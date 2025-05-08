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

/* ───────── embed ───────── */
//go:embed views/*.tmpl views/partials/*.tmpl
var resources embed.FS

/* ───────── DB rows ───────── */

type DailySummary struct{ /* unchanged */ }       // ↳ (see previous file)
type FoodEntry struct{ /* unchanged */ }

type BMI struct {
	LogDate time.Time `db:"log_date" json:"date"`
	Value   float64   `db:"bmi"      json:"bmi"`
}

type Weekly struct {
	WeekStart time.Time  `db:"week_start"`
	AvgWeight *float64   `db:"avg_weight"`
	AvgMood   *float64   `db:"avg_mood"`
	TotalKcal *int       `db:"total_kcal_est"`
}

/* ───────── helpers ───────── */

func fmtF2(f *float64) string { if f == nil { return "–" }; return fmt.Sprintf("%.1f", *f) }
func fmtInt(i *int) string    { if i == nil { return "–" }; return fmt.Sprintf("%d", *i) }
func safeHTML(s string) template.HTML { return template.HTML(s) }
func mod(a, b int) int                { return a % b }

/* ───────── page data ───────── */
type PageData struct {
	Summary []DailySummary
	Food    []FoodEntry
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
	mux.HandleFunc("/api/bmi", app.handleBMI)    // <-- new
	mux.HandleFunc("/weekly", app.handleWeekly)  // <-- new

	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

/* ───────── handlers (existing unchanged ones hidden) ───────── */
/* … handleIndex / handleLog / handleFood from v0.4 are unchanged … */

/* ---- BMI JSON endpoint ---- */
func (a *App) handleBMI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := a.db.Query(ctx,
		`SELECT log_date, bmi
		   FROM v_bmi
		  WHERE user_id = 1
		ORDER BY log_date`)
	if err != nil { http.Error(w, err.Error(), 500); return }

	var series []BMI
	for rows.Next() {
		var b BMI
		if err := rows.Scan(&b.LogDate, &b.Value); err != nil { continue }
		series = append(series, b)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(series)
}

/* ---- Weekly partial ---- */
func (a *App) handleWeekly(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var wk Weekly
	err := a.db.QueryRow(ctx, `
		SELECT week_start, avg_weight, avg_mood, total_kcal_est
		  FROM v_weekly_stats
		 WHERE user_id = 1
		   AND week_start = date_trunc('week', CURRENT_DATE)`).Scan(
		&wk.WeekStart, &wk.AvgWeight, &wk.AvgMood, &wk.TotalKcal)
	if err != nil { http.Error(w, err.Error(), 500); return }
	a.tpl.ExecuteTemplate(w, "weekly.tmpl", wk)
}

