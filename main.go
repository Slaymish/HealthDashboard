package main

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

// ───────────────────────────── resources ─────────────────────────────

//go:embed views/*.tmpl views/partials/*.tmpl
var resources embed.FS

// ───────────────────────────── types ─────────────────────────────

type App struct {
	db  *pgx.Conn
	tpl *template.Template
}

// row from v_daily_summary
type DailySummary struct {
	UserID            int        `db:"user_id"`
	LogDate           time.Time  `db:"log_date"`
	WeightKg          *float64   `db:"weight_kg"`
	KcalBudgeted      *int       `db:"kcal_budgeted"`
	KcalEstimated     *int       `db:"kcal_estimated"`
	Mood              *int       `db:"mood"`
	Motivation        *int       `db:"motivation"`
	TotalActivityMin  *int       `db:"total_activity_min"`
	SleepDurationMins *int       `db:"sleep_duration"`
}

// ───────────────────────────── helpers for templates ─────────────────────────────

func fmtF2(f *float64) string {
	if f == nil {
		return "–"
	}
	return fmt.Sprintf("%.1f", *f)
}
func fmtInt(i *int) string {
	if i == nil {
		return "–"
	}
	return fmt.Sprintf("%d", *i)
}

// ───────────────────────────── boot ─────────────────────────────

func main() {
	_ = godotenv.Load() // .env is optional

	conn, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("database connect: %v", err)
	}

	funcMap := template.FuncMap{"fmtF2": fmtF2, "fmtInt": fmtInt}
	tpl := template.Must(template.New("").Funcs(funcMap).
		ParseFS(resources, "views/*.tmpl", "views/partials/*.tmpl"))

	app := &App{db: conn, tpl: tpl}

	mux := http.NewServeMux()
	mux.HandleFunc("/", app.handleIndex)
	mux.HandleFunc("/log", app.handleLog) // HTMX POSTs land here

	log.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

// ───────────────────────────── handlers ─────────────────────────────

// render home page
func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := a.fetchSummary(r.Context())
	if err != nil {
		log.Printf("fetchSummary: %v", err)
		http.Error(w, err.Error(), 500)
		return
	}
	a.tpl.ExecuteTemplate(w, "index.tmpl", data)
}

// POST /log – weight, mood, sleep, etc.
func (a *App) handleLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}

	userID := 1 // TODO: auth
	// Ensure row exists for today
	_, _ = a.db.Exec(ctx,
		`INSERT INTO daily_logs (user_id, log_date)
		 VALUES ($1, CURRENT_DATE)
		 ON CONFLICT (user_id, log_date) DO NOTHING`, userID)

	// --- weight
	if v := r.FormValue("weight_kg"); v != "" {
		_, err := a.db.Exec(ctx,
			`UPDATE daily_logs SET weight_kg = $1
			 WHERE user_id=$2 AND log_date=CURRENT_DATE`, v, userID)
		if err != nil {
			log.Printf("update weight: %v", err)
		}
	}

	// --- mood (1–10)
	if v := r.FormValue("mood"); v != "" {
		_, err := a.db.Exec(ctx,
			`UPDATE daily_logs SET mood = $1
			 WHERE user_id=$2 AND log_date=CURRENT_DATE`, v, userID)
		if err != nil {
			log.Printf("update mood: %v", err)
		}
	}

	// --- sleep minutes
	if v := r.FormValue("sleep_min"); v != "" {
		min, _ := strconv.Atoi(v)
		if min > 0 {
			_, err := a.db.Exec(ctx,
				`UPDATE daily_logs SET sleep_duration = $1
				 WHERE user_id=$2 AND log_date=CURRENT_DATE`, min, userID)
			if err != nil {
				log.Printf("update sleep: %v", err)
			}
		}
	}

	// Return fresh table partial for HTMX swap
	a.renderSummary(w, ctx)
}

// ───────────────────────────── query helpers ─────────────────────────────

func (a *App) fetchSummary(ctx context.Context) ([]DailySummary, error) {
	rows, err := a.db.Query(ctx, `
		SELECT user_id,
		       log_date,
		       weight_kg,
		       kcal_budgeted,
		       kcal_estimated,
		       mood,
		       motivation,
		       total_activity_min,
		       sleep_duration
		FROM   v_daily_summary
		WHERE  user_id = 1
		ORDER  BY log_date DESC
		LIMIT  14`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DailySummary
	for rows.Next() {
		var d DailySummary
		if err := rows.Scan(
			&d.UserID, &d.LogDate, &d.WeightKg, &d.KcalBudgeted,
			&d.KcalEstimated, &d.Mood, &d.Motivation,
			&d.TotalActivityMin, &d.SleepDurationMins,
		); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (a *App) renderSummary(w http.ResponseWriter, ctx context.Context) {
	data, err := a.fetchSummary(ctx)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a.tpl.ExecuteTemplate(w, "daily_summary.tmpl", data)
}

