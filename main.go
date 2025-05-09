package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

/* ───────── embed templates ───────── */
//go:embed views/*.tmpl views/partials/*.tmpl
var resources embed.FS

/* ───────── DB structs ───────── */

type DailySummary struct {
	UserID        int        `db:"user_id"`
	LogDate       time.Time  `db:"log_date"`
	WeightKg      *float64   `db:"weight_kg"`
	KcalBudgeted  *int       `db:"kcal_budgeted"`
	KcalEstimated *int       `db:"kcal_estimated"`
	Mood          *int       `db:"mood"`
	Motivation    *int       `db:"motivation"`
}

type FoodEntry struct {
	EntryID   int
	Calories  int
	Note      *string
	CreatedAt time.Time
}

/* ───────── helpers ───────── */

func fmtF2(f *float64) string { if f == nil { return "–" }; return fmt.Sprintf("%.1f", *f) }
func fmtInt(i *int) string    { if i == nil { return "–" }; return fmt.Sprintf("%d", *i) }
func safeHTML(s string) template.HTML { return template.HTML(s) }
func mod(a, b int) int                { return a % b }
func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }


/* ───────── payloads ───────── */

type PageData struct {
	Pivot   time.Time
	Summary []DailySummary
	Food    []FoodEntry
	Quick   []QuickAdd
}

type SummaryPayload struct {
	Pivot   time.Time
	Summary []DailySummary
}

/* ───────── App ───────── */

type App struct {
	db  *pgxpool.Pool
	tpl *template.Template
}

/* ───────── main ───────── */

func main() {
	_ = godotenv.Load()

	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil { log.Fatalf("db pool: %v", err) }
	defer pool.Close()

	port := os.Getenv("PORT")

	funcs := template.FuncMap{
		"fmtF2": fmtF2, "fmtInt": fmtInt,
		"safeHTML": safeHTML, "mod": mod,
	}
	tpl := template.Must(template.New("").Funcs(funcs).
		ParseFS(resources, "views/*.tmpl", "views/partials/*.tmpl"))

	app := &App{db: pool, tpl: tpl}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/log", app.handleLog)
	mux.HandleFunc("/food", app.handleFood)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // 204, no body
	})

	mux.HandleFunc("/debug/summary", app.handleDebugSummary) // <-- new
	log.Println("▶️  Listening on ::" +port )
	log.Fatal(http.ListenAndServe(":" + port, mux))
}

/* ───────── util ───────── */

func parsePivot(r *http.Request) time.Time {
	if d := r.FormValue("date"); d != "" {
		if t, err := time.Parse("2006-01-02", d); err == nil { return t }
	}
	if d := r.URL.Query().Get("d"); d != "" {
		if t, err := time.Parse("2006-01-02", d); err == nil { return t }
	}
	return time.Now().Truncate(24 * time.Hour)
}

/* ───────── handlers ───────── */

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
    log.Printf("↩️ %s %s", r.Method, r.URL.String())
    if r.URL.Path == "/favicon.ico" { w.WriteHeader(204); return }

    pivot := parsePivot(r)
    log.Printf("➡️  / pivot=%s", pivot)

    ctx := r.Context()
    slice, _ := a.fetchSummary(ctx, pivot)
    food,  _ := a.fetchFood(ctx, pivot)

    if len(slice) == 0 {
        log.Printf("⚠️  table empty for %s", pivot)
    }

    // ── proper error handling ───────────────────────────────────────────────
    quick, _ := a.fetchQuickAdd(ctx)

    if err := a.tpl.ExecuteTemplate(
        w, "index.tmpl",
        PageData{Pivot: pivot, Summary: slice, Food: food,Quick:quick},
    ); err != nil {
        log.Printf("template error (index): %v", err)
        http.Error(w, "template rendering failed", http.StatusInternalServerError)
        return
    }
}



func (a *App) handleLog(w http.ResponseWriter, r *http.Request){
	log.Printf("↩️ %s %s", r.Method, r.URL.String())
	if r.Method!=http.MethodPost { http.Error(w,"method not allowed",405); return }
	_ = r.ParseForm()
	pivot := parsePivot(r)
	ctx,uid := r.Context(),1

	a.db.Exec(ctx, `INSERT INTO daily_logs (user_id,log_date)
	                VALUES ($1,$2) ON CONFLICT DO NOTHING`, uid,pivot)

	if v := r.FormValue("weight_kg"); v!="" {
		a.db.Exec(ctx,`UPDATE daily_logs SET weight_kg=$1
		              WHERE user_id=$2 AND log_date=$3`,v,uid,pivot)
	}

       if isHX(r) {
         // ① update food list in-place …
         rows, _ := a.fetchFood(ctx, pivot)
         a.tpl.ExecuteTemplate(w, "daily_summary.tmpl", rows)
 
         // ② …and send a *summary* refresh out-of-band
         data, _ := a.fetchSummary(ctx, pivot)
         w.Write([]byte("\n"))
         a.tpl.ExecuteTemplate(w, "summary_partial.tmpl", data)
         return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleFood(w http.ResponseWriter, r *http.Request) {
	log.Printf("↩️ %s %s", r.Method, r.URL.String())
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()

	pivot := parsePivot(r)
	ctx, uid := r.Context(), 1

	// ensure daily_logs row exists
	var logID int
	err := a.db.QueryRow(ctx,
		`INSERT INTO daily_logs (user_id, log_date)
		     VALUES ($1,$2)
		     ON CONFLICT (user_id,log_date) DO UPDATE SET log_date=EXCLUDED.log_date
		 RETURNING log_id`,
		uid, pivot).Scan(&logID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	cal, _ := strconv.Atoi(r.FormValue("calories"))
	if cal > 0 {
		_, err = a.db.Exec(ctx,
			`INSERT INTO daily_calorie_entries (log_id, calories, note)
			     VALUES ($1,$2,NULLIF($3,''))`,
			logID, cal, r.FormValue("note"))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}

	if isHX(r) {
	// ① update food list in-place …
         rows, _ := a.fetchFood(ctx, pivot)
         a.tpl.ExecuteTemplate(w, "food.tmpl", rows)
 
         // ② …and send a *summary* refresh out-of-band
         data, _ := a.fetchSummary(ctx, pivot)
         w.Write([]byte("\n"))
         a.tpl.ExecuteTemplate(w, "summary_partial.tmpl", data)
         return
     }
	    http.Redirect(w, r, "/", http.StatusSeeOther)
}
/* --- DEBUG endpoint --- */

func (a *App) handleDebugSummary(w http.ResponseWriter, r *http.Request){
	pivot := parsePivot(r)
	rows,_ := a.fetchSummary(r.Context(),pivot)
	w.Header().Set("Content-Type","application/json")
	json.NewEncoder(w).Encode(rows)
}

/* ───────── DB helpers ───────── */

func (a *App) fetchSummary(ctx context.Context, pivot time.Time) ([]DailySummary,error){
	start := pivot.AddDate(0,0,-6)
	end   := pivot.AddDate(0,0,7)
	log.Printf("SQL  window %s → %s", start, end)

	rows, err := a.db.Query(ctx, `
		SELECT user_id,log_date,weight_kg,kcal_budgeted,
		       kcal_estimated,mood,motivation
		  FROM v_daily_summary
		 WHERE user_id=1
		   AND log_date BETWEEN $1 AND $2
		 ORDER BY log_date`, start,end)
	if err!=nil { return nil,err }
	defer rows.Close()

	var out []DailySummary
	for rows.Next() {
		var d DailySummary
		if err := rows.Scan(&d.UserID,&d.LogDate,&d.WeightKg,
		                    &d.KcalBudgeted,&d.KcalEstimated,
		                    &d.Mood,&d.Motivation); err!=nil {
			log.Printf("❌ scan: %v", err); continue
		}
		out = append(out,d)
	}
	log.Printf("✅ fetched %d rows", len(out))
	if len(out) > 0 {
	    log.Printf("📝 first date: %v", out[0].LogDate)
	}

	return out,nil
}

func (a *App) fetchFood(ctx context.Context, pivot time.Time) ([]FoodEntry,error){
	rows,err := a.db.Query(ctx,`
		SELECT e.entry_id,e.calories,e.note,e.created_at
		  FROM daily_calorie_entries e
		  JOIN daily_logs d ON d.log_id=e.log_id
		 WHERE d.user_id=1 AND d.log_date=$1
		 ORDER BY e.created_at DESC`,pivot)
	if err!=nil { return nil,err }
	defer rows.Close()

	var out []FoodEntry
	for rows.Next(){
		var f FoodEntry
		rows.Scan(&f.EntryID,&f.Calories,&f.Note,&f.CreatedAt)
		out=append(out,f)
	}
	return out,nil
}

type QuickAdd struct{ Calories int; Note *string }

func (a *App) fetchQuickAdd(ctx context.Context) ([]QuickAdd, error) {
	rows, err := a.db.Query(ctx, `
	    SELECT calories, note
	      FROM daily_calorie_entries
	     WHERE user_id = 1
	     GROUP BY calories, note
	     ORDER BY COUNT(*) DESC, MAX(created_at) DESC
	     LIMIT 6`)
	if err != nil { return nil, err }
	defer rows.Close()

	var out []QuickAdd
	for rows.Next() {
		var q QuickAdd
		rows.Scan(&q.Calories, &q.Note)
		out = append(out, q)
	}
	return out, nil
}

func (a *App) renderSummary(w http.ResponseWriter, ctx context.Context, pivot time.Time) {
    rows, _ := a.fetchSummary(ctx, pivot)
    if err := a.tpl.ExecuteTemplate(
        w, "daily_summary.tmpl",
        SummaryPayload{Pivot: pivot, Summary: rows},
    ); err != nil {
        log.Printf("template error (summary): %v", err)
    }
}

func (a *App) renderFood(w http.ResponseWriter, ctx context.Context, pivot time.Time) {
    rows, _ := a.fetchFood(ctx, pivot)
    if err := a.tpl.ExecuteTemplate(w, "food.tmpl", rows); err != nil {
        log.Printf("template error (food): %v", err)
    }
}


