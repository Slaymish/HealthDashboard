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
	LogDate          time.Time `json:"log_date"`
	WeightKg         *float64  `json:"weight_kg,omitempty"`
	KcalEstimated    *int      `json:"kcal_estimated,omitempty"`
	KcalBudgeted     *int      `json:"kcal_budgeted,omitempty"`
	Mood             *int      `json:"mood,omitempty"`
	Motivation       *int      `json:"motivation,omitempty"`
	TotalActivityMin *int      `json:"total_activity_min,omitempty"`
	SleepDuration    *int      `json:"sleep_duration,omitempty"`
}

// in your data structs
type FoodEntry struct {
	ID        int // ← new
	CreatedAt time.Time
	Calories  int
	Note      sql.NullString
}

type QuickAddItem struct {
	Calories int
	Note     string
}

type BMI struct {
	LogDate time.Time `json:"date"`
	Value   float64   `json:"bmi"`
}

type Weekly struct {
	WeekStart      time.Time `json:"week_start"`
	AvgWeight      *float64  `json:"avg_weight,omitempty"`
	TotalEstimated *int      `json:"total_estimated,omitempty"`
	TotalBudgeted  *int      `json:"total_budgeted,omitempty"`
	TotalDeficit   *int      `json:"total_deficit,omitempty"`
}

type PageData struct {
	Pivot    time.Time // new
	Summary  []DailySummary
	Food     []FoodEntry
	QuickAdd []QuickAddItem
}

// For POST /api/log/weight
type WeightLogRequest struct {
	WeightKg float64 `json:"weight_kg"`
	Date     string  `json:"date,omitempty"` // YYYY-MM-DD, defaults to today
}

type WeightLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// For POST /api/log/calorie
type CalorieLogRequest struct {
	Calories int    `json:"calories"`
	Note     string `json:"note,omitempty"`
	Date     string `json:"date,omitempty"` // YYYY-MM-DD, defaults to today
}

type CalorieLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// For POST /api/log/cardio
type CardioLogRequest struct {
	DurationMin int    `json:"duration_min"`
	Date        string `json:"date,omitempty"` // YYYY-MM-DD, defaults to today
}

type CardioLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// For POST /api/log/mood
type MoodLogRequest struct {
	Mood int    `json:"mood"`
	Date string `json:"date,omitempty"` // YYYY-MM-DD, defaults to today
}

type MoodLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// For GET /api/calories/today
type CaloriesTodayResponse struct {
	Date          string `json:"date"`
	TotalCalories int    `json:"total_calories"`
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
func mod(a, b int) int                { return a % b }
func todayStr() string                { return time.Now().Format("2006-01-02") }

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
	mux.HandleFunc("/api/log/weight", app.handleLogWeight)
	mux.HandleFunc("/api/log/calorie", app.handleLogCalorie)
	mux.HandleFunc("/api/log/cardio", app.handleLogCardio)
	mux.HandleFunc("/api/log/mood", app.handleLogMood)
	mux.HandleFunc("/api/summary/daily", app.handleGetDailySummary)
	mux.HandleFunc("/api/calories/today", app.handleGetCaloriesToday)
	mux.HandleFunc("/api/summary/weekly", app.handleGetWeeklySummary) // New route
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
	end := pivot.AddDate(0, 0, span)

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
			d                              DailySummary
			weight                         sql.NullFloat64
			est, bud, mood, motiv, act, sl sql.NullInt32
		)
		if err := rows.Scan(
			&d.LogDate, &weight, &est, &bud,
			&mood, &motiv, &act, &sl); err != nil {
			return nil, err
		}
		if weight.Valid {
			v := weight.Float64
			d.WeightKg = &v
		}
		if est.Valid {
			v := int(est.Int32)
			d.KcalEstimated = &v
		}
		if bud.Valid {
			v := int(bud.Int32)
			d.KcalBudgeted = &v
		}
		if mood.Valid {
			v := int(mood.Int32)
			d.Mood = &v
		}
		if motiv.Valid {
			v := int(motiv.Int32)
			d.Motivation = &v
		}
		if act.Valid {
			v := int(act.Int32)
			d.TotalActivityMin = &v
		}
		if sl.Valid {
			v := int(sl.Int32)
			d.SleepDuration = &v
		}

		out = append(out, d)
	}
	return out, rows.Err()
}

func (a *App) fetchFood(ctx context.Context) ([]FoodEntry, error) {
	rows, err := a.db.Query(ctx, `
		SELECT e.entry_id, e.created_at, e.calories, e.note
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
		if err := rows.Scan(&f.ID, &f.CreatedAt, &f.Calories, &f.Note); err != nil {
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

func (a *App) fetchSingleDaySummary(ctx context.Context, date time.Time, userID int) (DailySummary, error) {
	var summary DailySummary
	summary.LogDate = date // Set the date initially

	var (
		weight                         sql.NullFloat64
		est, bud, mood, motiv, act, sl sql.NullInt32
	)

	err := a.db.QueryRow(ctx, `
        SELECT weight_kg, kcal_estimated, kcal_budgeted,
               mood, motivation, total_activity_min, sleep_duration
          FROM v_daily_summary
         WHERE user_id = $1 AND log_date = $2`,
		userID, date).Scan(
		&weight, &est, &bud,
		&mood, &motiv, &act, &sl,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			// Return summary with only LogDate set, other fields will be nil
			return summary, nil // Not an application error if no data for date
		}
		return summary, err // Actual database error
	}

	// Populate the summary struct
	if weight.Valid {
		v := weight.Float64
		summary.WeightKg = &v
	}
	if est.Valid {
		v := int(est.Int32)
		summary.KcalEstimated = &v
	}
	if bud.Valid {
		v := int(bud.Int32)
		summary.KcalBudgeted = &v
	}
	if mood.Valid {
		v := int(mood.Int32)
		summary.Mood = &v
	}
	if motiv.Valid {
		v := int(motiv.Int32)
		summary.Motivation = &v
	}
	if act.Valid {
		v := int(act.Int32)
		summary.TotalActivityMin = &v
	}
	if sl.Valid {
		v := int(sl.Int32)
		summary.SleepDuration = &v
	}

	return summary, nil
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

	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	foods, _ := a.fetchFood(ctx) // today’s food; unchanged
	quick, _ := a.fetchQuickAdd(ctx)

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
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}

	userID := 1
	_, _ = a.db.Exec(ctx, `INSERT INTO daily_logs (user_id, log_date)
	                       VALUES ($1, CURRENT_DATE)
	                       ON CONFLICT (user_id, log_date) DO NOTHING`, userID)

	update := func(col, formKey string) {
		val := r.FormValue(formKey)
		if val == "" {
			return
		}
		_, err := a.db.Exec(ctx, fmt.Sprintf(
			`UPDATE daily_logs SET %s = $1 WHERE user_id = $2 AND log_date = CURRENT_DATE`, col),
			val, userID)
		if err != nil {
			log.Printf("update %s: %v", col, err)
		}
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
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}

	calStr := r.FormValue("calories")
	cal, err := strconv.Atoi(calStr)
	if err != nil || cal < 0 {
		http.Error(w, "calories", 400)
		return
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
		http.Error(w, err.Error(), 500)
		return
	}

	_, err = a.db.Exec(ctx, `
		INSERT INTO daily_calorie_entries (log_id, calories, note)
		VALUES ($1, $2, NULLIF($3,''))`, logID, cal, note)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/", 303)
		return
	}

	foods, _ := a.fetchFood(ctx)
	sum, _ := a.fetchSummary(ctx, time.Now(), 3)

	var foodHTML, sumHTML strings.Builder
	_ = a.tpl.ExecuteTemplate(&foodHTML, "food.tmpl", foods)
	_ = a.tpl.ExecuteTemplate(&sumHTML, "summary_partial.tmpl", sum)

	summaryFrag := strings.Replace(sumHTML.String(),
		`id="summary"`, `id="summary" hx-swap-oob="outerHTML"`, 1)

	fmt.Fprint(w, foodHTML.String(), "\n", summaryFrag)
}

func (a *App) handleBMI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	const sql = `
    SELECT d.dt AS log_date, b.bmi AS value
    FROM generate_series(
       CURRENT_DATE - INTERVAL '29 days',
       CURRENT_DATE,
       '1 day'
    ) AS d(dt)
    LEFT JOIN v_bmi AS b
      ON b.log_date = d.dt AND b.user_id = $1
    ORDER BY d.dt;
  `
	rows, err := a.db.Query(ctx, sql, 1)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	type BMI struct {
		LogDate time.Time `json:"date"`
		Value   *float64  `json:"bmi"`
	}

	series := make([]BMI, 0, 30)
	for rows.Next() {
		var b BMI
		if err := rows.Scan(&b.LogDate, &b.Value); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		series = append(series, b)
	}

	log.Println(series)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(series)
}

func (a *App) handleWeekly(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var wk Weekly
	err := a.db.QueryRow(ctx, `
		SELECT week_start, avg_weight, total_budgeted, total_estimated, total_deficit
		  FROM v_weekly_stats
		 WHERE user_id = 1
		   AND week_start = date_trunc('week', CURRENT_DATE)`).
		Scan(&wk.WeekStart, &wk.AvgWeight, &wk.TotalBudgeted, &wk.TotalEstimated, &wk.TotalDeficit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = a.tpl.ExecuteTemplate(w, "weekly.tmpl", wk)
}

// handleLogWeight handles POST /api/log/weight
func (a *App) handleLogWeight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var reqPayload WeightLogRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		log.Printf("Error decoding weight log payload: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}

	if reqPayload.WeightKg <= 0 {
		log.Printf("Invalid weight_kg value: %f", reqPayload.WeightKg)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "weight_kg must be a positive value"})
		return
	}

	logDate := time.Now().Format("2006-01-02")
	if reqPayload.Date != "" {
		parsedDate, err := time.Parse("2006-01-02", reqPayload.Date)
		if err != nil {
			log.Printf("Invalid date format for %s: %v", reqPayload.Date, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Invalid date format. Please use YYYY-MM-DD."})
			return
		}
		logDate = parsedDate.Format("2006-01-02")
	}

	userID := 1 // Hardcoded user_id as per existing patterns

	// Get or create log_id
	var logID int
	err := a.db.QueryRow(ctx, `
		INSERT INTO daily_logs (user_id, log_date)
		VALUES ($1, $2)
		ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date
		RETURNING log_id`, userID, logDate).Scan(&logID)

	if err != nil {
		log.Printf("Error upserting daily_log for user %d, date %s: %v", userID, logDate, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Database error while preparing log entry."})
		return
	}

	// Update weight_kg in daily_logs table (as per handleLog example)
	// The schema seems to have weight_kg directly in daily_logs, not a separate logs_metrics table.
	// The handleLog function updates daily_logs directly.
	_, err = a.db.Exec(ctx,
		`UPDATE daily_logs SET weight_kg = $1 WHERE log_id = $2 AND user_id = $3`,
		reqPayload.WeightKg, logID, userID)

	if err != nil {
		log.Printf("Error updating weight_kg for log_id %d: %v", logID, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Database error while updating weight."})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(WeightLogResponse{Success: true, Message: "Weight logged successfully"})
}

// handleLogCalorie handles POST /api/log/calorie
func (a *App) handleLogCalorie(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var reqPayload CalorieLogRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		log.Printf("Error decoding calorie log payload: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}

	if reqPayload.Calories < 0 {
		log.Printf("Invalid calories value: %d", reqPayload.Calories)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "calories must be a non-negative value"})
		return
	}

	logDate := time.Now().Format("2006-01-02")
	if reqPayload.Date != "" {
		parsedDate, err := time.Parse("2006-01-02", reqPayload.Date)
		if err != nil {
			log.Printf("Invalid date format for %s: %v", reqPayload.Date, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "Invalid date format. Please use YYYY-MM-DD."})
			return
		}
		logDate = parsedDate.Format("2006-01-02")
	}

	userID := 1 // Hardcoded user_id

	// Get or create log_id
	var logID int
	err := a.db.QueryRow(ctx, `
		INSERT INTO daily_logs (user_id, log_date)
		VALUES ($1, $2)
		ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date
		RETURNING log_id`, userID, logDate).Scan(&logID)

	if err != nil {
		log.Printf("Error upserting daily_log for user %d, date %s: %v", userID, logDate, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "Database error while preparing log entry."})
		return
	}

	// Insert calorie entry
	_, err = a.db.Exec(ctx, `
		INSERT INTO daily_calorie_entries (log_id, calories, note)
		VALUES ($1, $2, NULLIF($3,''))`, logID, reqPayload.Calories, reqPayload.Note)
	if err != nil {
		log.Printf("Error inserting calorie entry for log_id %d: %v", logID, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "Database error while logging calorie entry."})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(CalorieLogResponse{Success: true, Message: "Calorie entry logged successfully"})
}

// handleLogCardio handles POST /api/log/cardio
func (a *App) handleLogCardio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var reqPayload CardioLogRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		log.Printf("Error decoding cardio log payload: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}

	if reqPayload.DurationMin < 0 {
		log.Printf("Invalid duration_min value: %d", reqPayload.DurationMin)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "duration_min must be a non-negative value"})
		return
	}

	logDate := time.Now().Format("2006-01-02")
	if reqPayload.Date != "" {
		parsedDate, err := time.Parse("2006-01-02", reqPayload.Date)
		if err != nil {
			log.Printf("Invalid date format for %s: %v", reqPayload.Date, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "Invalid date format. Please use YYYY-MM-DD."})
			return
		}
		logDate = parsedDate.Format("2006-01-02")
	}

	userID := 1 // Hardcoded user_id

	// Get or create log_id
	var logID int
	err := a.db.QueryRow(ctx, `
		INSERT INTO daily_logs (user_id, log_date)
		VALUES ($1, $2)
		ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date
		RETURNING log_id`, userID, logDate).Scan(&logID)

	if err != nil {
		log.Printf("Error upserting daily_log for user %d, date %s: %v", userID, logDate, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "Database error while preparing log entry."})
		return
	}

	// Update total_activity_min in daily_logs
	// COALESCE is used to treat NULL as 0 when adding duration
	_, err = a.db.Exec(ctx, `
		UPDATE daily_logs
		SET total_activity_min = COALESCE(total_activity_min, 0) + $1
		WHERE log_id = $2 AND user_id = $3`,
		reqPayload.DurationMin, logID, userID)

	if err != nil {
		log.Printf("Error updating total_activity_min for log_id %d: %v", logID, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "Database error while logging cardio activity."})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(CardioLogResponse{Success: true, Message: "Cardio activity logged successfully"})
}

// handleLogMood handles POST /api/log/mood
func (a *App) handleLogMood(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var reqPayload MoodLogRequest
	// The request body should contain the mood value as an integer.
	// A field like "value" or "mood_level" might be expected by the client.
	// For now, sticking to "mood" as per struct.
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		log.Printf("Error decoding mood log payload: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}

	// Optional: Add validation for mood value if specific range is known (e.g., 1-5)
	// if reqPayload.Mood < 1 || reqPayload.Mood > 5 {
	// 	log.Printf("Invalid mood value: %d", reqPayload.Mood)
	// 	w.Header().Set("Content-Type", "application/json")
	// 	w.WriteHeader(http.StatusBadRequest)
	// 	json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Mood value out of range."})
	// 	return
	// }

	logDate := time.Now().Format("2006-01-02")
	if reqPayload.Date != "" {
		parsedDate, err := time.Parse("2006-01-02", reqPayload.Date)
		if err != nil {
			log.Printf("Invalid date format for %s: %v", reqPayload.Date, err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Invalid date format. Please use YYYY-MM-DD."})
			return
		}
		logDate = parsedDate.Format("2006-01-02")
	}

	userID := 1 // Hardcoded user_id

	var logID int
	err := a.db.QueryRow(ctx, `
		INSERT INTO daily_logs (user_id, log_date)
		VALUES ($1, $2)
		ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date
		RETURNING log_id`, userID, logDate).Scan(&logID)

	if err != nil {
		log.Printf("Error upserting daily_log for user %d, date %s: %v", userID, logDate, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Database error while preparing log entry."})
		return
	}

	_, err = a.db.Exec(ctx,
		`UPDATE daily_logs SET mood = $1 WHERE log_id = $2 AND user_id = $3`,
		reqPayload.Mood, logID, userID)

	if err != nil {
		log.Printf("Error updating mood for log_id %d: %v", logID, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Database error while logging mood."})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(MoodLogResponse{Success: true, Message: "Mood logged successfully"})
}

// handleGetDailySummary handles GET /api/summary/daily?date=YYYY-MM-DD
func (a *App) handleGetDailySummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	dateStr := r.URL.Query().Get("date")
	var queryDate time.Time
	var err error

	if dateStr == "" {
		queryDate = time.Now()
	} else {
		queryDate, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			log.Printf("Invalid date format query parameter: %s, error: %v", dateStr, err)
			// Default to today if parsing fails (as per requirement: "Default to today if not provided or invalid")
			queryDate = time.Now()
		}
	}
	// Normalize queryDate to just YYYY-MM-DD for consistency in fetching and response
	queryDate = time.Date(queryDate.Year(), queryDate.Month(), queryDate.Day(), 0, 0, 0, 0, queryDate.Location())


	userID := 1 // Hardcoded user_id

	summary, err := a.fetchSingleDaySummary(ctx, queryDate, userID)
	if err != nil {
		// This implies an actual DB error, not sql.ErrNoRows (handled in fetchSingleDaySummary)
		log.Printf("Error fetching single day summary for user %d, date %s: %v", userID, queryDate.Format("2006-01-02"), err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		// Using MoodLogResponse for generic error, consider a more generic error struct if many such endpoints
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Error fetching daily summary."})
		return
	}

	// The summary.LogDate is already set by fetchSingleDaySummary correctly to the queryDate.
	// If no record was found, the metric fields in summary will be nil.

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(summary)
}

// handleGetCaloriesToday handles GET /api/calories/today
func (a *App) handleGetCaloriesToday(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	currentDate := time.Now()
	userID := 1 // Hardcoded user_id

	var totalCalories int
	err := a.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(e.calories), 0)
		  FROM daily_calorie_entries e
		  JOIN daily_logs dl ON e.log_id = dl.log_id
		 WHERE dl.user_id = $1 AND dl.log_date = $2`,
		userID, currentDate.Format("2006-01-02")).Scan(&totalCalories)

	if err != nil {
		// Log the error and return a generic server error response
		log.Printf("Error fetching total calories for user %d, date %s: %v", userID, currentDate.Format("2006-01-02"), err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		// Using MoodLogResponse for a generic error structure; consider a dedicated one if this pattern repeats.
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Error fetching total calories."})
		return
	}

	response := CaloriesTodayResponse{
		Date:          currentDate.Format("2006-01-02"),
		TotalCalories: totalCalories,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleGetWeeklySummary handles GET /api/summary/weekly?start_date=YYYY-MM-DD
func (a *App) handleGetWeeklySummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	dateStr := r.URL.Query().Get("start_date")
	var weekStartDate time.Time
	var err error
	userID := 1 // Hardcoded user_id

	if dateStr == "" {
		// Default to the start of the current week by querying the DB
		err = a.db.QueryRow(ctx, `SELECT date_trunc('week', CURRENT_DATE);`).Scan(&weekStartDate)
		if err != nil {
			log.Printf("Error fetching default week start date: %v", err)
			http.Error(w, "Error determining current week start date.", http.StatusInternalServerError)
			return
		}
	} else {
		weekStartDate, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			log.Printf("Invalid start_date format query parameter: %s, error: %v", dateStr, err)
			// Default to start of current week if parsing fails
			err = a.db.QueryRow(ctx, `SELECT date_trunc('week', CURRENT_DATE);`).Scan(&weekStartDate)
			if err != nil {
				log.Printf("Error fetching default week start date after parse failure: %v", err)
				http.Error(w, "Error determining current week start date.", http.StatusInternalServerError)
				return
			}
		}
		// Ensure the provided date is actually a week start date (e.g. a Monday)
		// This can be done by truncating it to the week.
		var actualWeekStartForProvidedDate time.Time
		err = a.db.QueryRow(ctx, `SELECT date_trunc('week', $1::date);`, weekStartDate).Scan(&actualWeekStartForProvidedDate)
		if err != nil {
			log.Printf("Error truncating provided start_date %s: %v", weekStartDate.Format("2006-01-02"), err)
			http.Error(w, "Error processing provided start_date.", http.StatusInternalServerError)
			return
		}
		weekStartDate = actualWeekStartForProvidedDate
	}

	var weeklySummary Weekly
	// Set WeekStart in the response, even if no other data is found
	weeklySummary.WeekStart = time.Date(weekStartDate.Year(), weekStartDate.Month(), weekStartDate.Day(), 0,0,0,0, time.UTC)


	err = a.db.QueryRow(ctx, `
		SELECT avg_weight, total_budgeted, total_estimated, total_deficit
		  FROM v_weekly_stats
		 WHERE user_id = $1 AND week_start = $2`,
		userID, weeklySummary.WeekStart).Scan(
		&weeklySummary.AvgWeight,
		&weeklySummary.TotalBudgeted,
		&weeklySummary.TotalEstimated,
		&weeklySummary.TotalDeficit,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			// No data for this week, return the weeklySummary struct with WeekStart and nil/zero values for metrics
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(weeklySummary)
			return
		}
		// Actual database error
		log.Printf("Error fetching weekly summary for user %d, week_start %s: %v", userID, weeklySummary.WeekStart.Format("2006-01-02"), err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Error fetching weekly summary."}) // Using generic error response
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(weeklySummary)
}
