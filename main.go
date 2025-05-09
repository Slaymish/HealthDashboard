package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

/* ───────────────────── Embeds ───────────────────── */

//go:embed views/*.tmpl views/partials/*.tmpl
var resources embed.FS

/* ───────────────────── Data structs ───────────────────── */

type DailySummary struct {
	LogDate          time.Time
	WeightKg         *float64
	KcalEstimated    *int
	KcalBudgeted     *int
	Mood             *int
	Motivation       *int
	TotalActivityMin *int
	SleepDuration    *int
}

type FoodEntry struct {
	CreatedAt time.Time
	Calories  int
	Note      sql.NullString
}

type QuickAddItem struct {
	Calories int
	Note     string
}

type BMI struct {
	LogDate time.Time  `json:"date"`
	Value   float64    `json:"bmi"`
}

type Weekly struct {
	WeekStart time.Time
	AvgWeight *float64
	AvgMood   *float64
	TotalKcal *int
}

type PageData struct {
    Pivot    time.Time        // new
    Summary  []DailySummary
    Food     []FoodEntry
    QuickAdd []QuickAddItem
}



/* ───────────────────── Helpers for templates ───────────────────── */

func fmtF2(p *float64) string {
	if p == nil {
		return "–"
	}
	return fmt.Sprintf("%.1f", *p)
}
func fmtInt(p *int) string {
	if p == nil {
		return "–"
	}
	return fmt.Sprintf("%d", *p)
}
func safeHTML(s string) template.HTML { return template.HTML(s) }
func mod(a, b int) int               { return a % b }
func todayStr() string               { return time.Now().Format("2006-01-02") }

/* ───────────────────── Core app ───────────────────── */

type App struct {
	db  *pgxpool.Pool
	tpl *template.Template
}

func main() {
	_ = godotenv.Load()

	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close()

	funcs := template.FuncMap{
		"fmtF2":    fmtF2,
		"fmtInt":   fmtInt,
		"safeHTML": safeHTML,
		"mod":      mod,
		"todayStr": todayStr,
	}
	tpl := template.Must(template.New("").Funcs(funcs).ParseFS(
		resources, "views/*.tmpl", "views/partials/*.tmpl"))

	app := &App{db: pool, tpl: tpl}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/log", app.handleLog)
	mux.HandleFunc("/food", app.handleFood)
	mux.HandleFunc("/api/bmi", app.handleBMI)
	mux.HandleFunc("/weekly", app.handleWeekly)

	server := &http.Server{
		Addr:    ":8181",
		Handler: mux,
	}

	go func() {
		log.Println("Listening on :8181")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	// Graceful shutdown on Ctrl-C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("Shutting down …")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

/* ───────────────────── DB helpers ───────────────────── */

func (a *App) fetchSummary(ctx context.Context, pivot time.Time, span int) ([]DailySummary, error) {
	start := pivot.AddDate(0, 0, -span)
	end   := pivot.AddDate(0, 0,  span)

    rows, err := a.db.Query(ctx, `
        SELECT log_date, weight_kg, kcal_estimated, kcal_budgeted,
               mood, motivation, total_activity_min, sleep_duration
          FROM v_daily_summary
         WHERE user_id = 1
           AND log_date BETWEEN $1 AND $2
         ORDER BY log_date`,
        start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DailySummary
	for rows.Next() {
		var (
			d                             DailySummary
			weight                        sql.NullFloat64
			est, bud, mood, motiv, act, sl sql.NullInt32
		)
		if err := rows.Scan(
			&d.LogDate, &weight, &est, &bud,
			&mood, &motiv, &act, &sl); err != nil {
			return nil, err
		}
		if weight.Valid { v := weight.Float64; d.WeightKg = &v }
		if est.Valid    { v := int(est.Int32); d.KcalEstimated = &v }
		if bud.Valid    { v := int(bud.Int32); d.KcalBudgeted  = &v }
		if mood.Valid   { v := int(mood.Int32); d.Mood         = &v }
		if motiv.Valid  { v := int(motiv.Int32); d.Motivation  = &v }
		if act.Valid    { v := int(act.Int32); d.TotalActivityMin = &v }
		if sl.Valid     { v := int(sl.Int32);  d.SleepDuration    = &v }

		out = append(out, d)
	}
	return out, rows.Err()
}

func (a *App) fetchFood(ctx context.Context) ([]FoodEntry, error) {
	rows, err := a.db.Query(ctx, `
		SELECT e.created_at, e.calories, e.note
		  FROM daily_calorie_entries e
		  JOIN daily_logs l ON l.log_id = e.log_id
		 WHERE l.user_id = 1
		   AND l.log_date = CURRENT_DATE
		 ORDER BY e.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FoodEntry
	for rows.Next() {
		var f FoodEntry
		if err := rows.Scan(&f.CreatedAt, &f.Calories, &f.Note); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (a *App) fetchQuickAdd(ctx context.Context) ([]QuickAddItem, error) {
	rows, err := a.db.Query(ctx, `
		SELECT COALESCE(NULLIF(e.note,''),'') AS note, e.calories
		  FROM daily_calorie_entries e
		  JOIN daily_logs l ON l.log_id = e.log_id
		 WHERE l.user_id = 1
		 GROUP BY COALESCE(NULLIF(e.note,''),''), e.calories
		 ORDER BY COUNT(*) DESC, MAX(e.created_at) DESC
		 LIMIT 5`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []QuickAddItem
	for rows.Next() {
		var qi QuickAddItem
		if err := rows.Scan(&qi.Note, &qi.Calories); err != nil {
			return nil, err
		}
		out = append(out, qi)
	}
	return out, rows.Err()
}

/* ───────────────────── Handlers ───────────────────── */

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    pivot := time.Now()
    if qs := r.URL.Query().Get("d"); qs != "" {
        if p, err := time.Parse("2006-01-02", qs); err == nil {
            pivot = p
        }
    }

    summary, err := a.fetchSummary(ctx, pivot, 3)

    if err != nil { http.Error(w, err.Error(), 500); return }

    foods, _  := a.fetchFood(ctx)      // today’s food; unchanged
    quick, _  := a.fetchQuickAdd(ctx)

    data := PageData{
        Pivot:    pivot,
        Summary:  summary,
        Food:     foods,
        QuickAdd: quick,
    }
    _ = a.tpl.ExecuteTemplate(w, "index.tmpl", data)
}


func (a *App) handleLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost { http.Error(w, "method", 405); return }
	if err := r.ParseForm(); err != nil { http.Error(w, "bad form", 400); return }

	userID := 1
	_, _ = a.db.Exec(ctx, `INSERT INTO daily_logs (user_id, log_date)
	                       VALUES ($1, CURRENT_DATE)
	                       ON CONFLICT (user_id, log_date) DO NOTHING`, userID)

	update := func(col, formKey string) {
		val := r.FormValue(formKey)
		if val == "" { return }
		_, err := a.db.Exec(ctx, fmt.Sprintf(
			`UPDATE daily_logs SET %s = $1 WHERE user_id = $2 AND log_date = CURRENT_DATE`, col),
			val, userID)
		if err != nil { log.Printf("update %s: %v", col, err) }
	}
	update("weight_kg", "weight_kg")
	update("mood", "mood")
	update("sleep_duration", "sleep_min")

	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/", 303)
		return
	}
	sum, _ := a.fetchSummary(ctx, time.Now(), 3)
	_ = a.tpl.ExecuteTemplate(w, "summary_partial.tmpl", sum)
}

func (a *App) handleFood(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost { http.Error(w, "method", 405); return }
	if err := r.ParseForm(); err != nil { http.Error(w, "bad form", 400); return }

	calStr := r.FormValue("calories")
	cal, err := strconv.Atoi(calStr)
	if err != nil || cal < 0 {
		http.Error(w, "calories", 400); return
	}
	note := r.FormValue("note")
	userID := 1

	var logID int
	if err := a.db.QueryRow(ctx, `
		INSERT INTO daily_logs (user_id, log_date)
		VALUES ($1, CURRENT_DATE)
		ON CONFLICT (user_id, log_date) DO UPDATE
		SET log_date = EXCLUDED.log_date
		RETURNING log_id`, userID).Scan(&logID); err != nil {
		http.Error(w, err.Error(), 500); return
	}

	_, err = a.db.Exec(ctx, `
		INSERT INTO daily_calorie_entries (log_id, calories, note)
		VALUES ($1, $2, NULLIF($3,''))`, logID, cal, note)
	if err != nil { http.Error(w, err.Error(), 500); return }

	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/", 303); return
	}

	foods, _ := a.fetchFood(ctx)
	sum, _ := a.fetchSummary(ctx, time.Now(), 3)


	var foodHTML, sumHTML strings.Builder
	_ = a.tpl.ExecuteTemplate(&foodHTML, "food.tmpl", foods)
	_ = a.tpl.ExecuteTemplate(&sumHTML,  "summary_partial.tmpl", sum)

	summaryFrag := strings.Replace(sumHTML.String(),
		`id="summary"`, `id="summary" hx-swap-oob="outerHTML"`, 1)

	fmt.Fprint(w, foodHTML.String(), "\n", summaryFrag)
}

func (a *App) handleBMI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := a.db.Query(ctx, `
		SELECT log_date, bmi FROM v_bmi
		  WHERE user_id = 1
		  ORDER BY log_date`)
	if err != nil { http.Error(w, err.Error(), 500); return }
	defer rows.Close()
	var series []BMI
	for rows.Next() {
		var b BMI
		if err := rows.Scan(&b.LogDate, &b.Value); err == nil {
			series = append(series, b)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(series)
}

func (a *App) handleWeekly(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var wk Weekly
	err := a.db.QueryRow(ctx, `
		SELECT week_start, avg_weight, avg_mood, total_kcal_est
		  FROM v_weekly_stats
		 WHERE user_id = 1
		   AND week_start = date_trunc('week', CURRENT_DATE)`).
		Scan(&wk.WeekStart, &wk.AvgWeight, &wk.AvgMood, &wk.TotalKcal)
	if err != nil { http.Error(w, err.Error(), 500); return }
	_ = a.tpl.ExecuteTemplate(w, "weekly.tmpl", wk)
}
