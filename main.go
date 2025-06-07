package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

/* ───────────────────── Embeds ───────────────────── */

//go:embed views/*.tmpl views/partials/*.tmpl
var resources embed.FS

/* ───────────────────── Data structs ───────────────────── */

// DailySummary holds aggregated metrics for a single day.
// Used in UI for daily and 7-day views, and in API responses.
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

// FoodEntry represents a single food item logged by the user for a specific day.
// The 'ID' field is used to uniquely identify entries, e.g., for deletion (though not implemented).
type FoodEntry struct {
	ID        int // ← new
	CreatedAt time.Time
	Calories  int
	Note      sql.NullString
}

// QuickAddItem is used to populate the "Quick Add" food items list.
// These are derived from frequently logged food items.
type QuickAddItem struct {
	Calories int
	Note     string
}

// BMI represents a Body Mass Index value for a specific date.
// Used for the 30-day BMI trend chart.
type BMI struct {
	LogDate time.Time `json:"date"`
	Value   *float64  `json:"bmi"` // Changed to pointer
}

// Weekly holds summarized weekly statistics.
// Used for the weekly summary view and API endpoint.
type Weekly struct {
	WeekStart      time.Time `json:"week_start"`
	AvgWeight      *float64  `json:"avg_weight,omitempty"`
	TotalEstimated *int      `json:"total_estimated,omitempty"`
	TotalBudgeted  *int      `json:"total_budgeted,omitempty"`
	TotalDeficit   *int      `json:"total_deficit,omitempty"`
}

// GoalProjection holds estimated timeframes for reaching weight goals based on
// recent weight trends.
type GoalProjection struct {
	CurrentWeight    float64
	DailyChange      float64
	MilestoneWeight  float64
	MilestoneDays    *int
	MilestoneDate    *time.Time
	MilestoneFormula string
	GoalWeight       float64
	GoalDays         *int
	GoalDate         *time.Time
	GoalFormula      string
}

// PageData is the primary data structure passed to HTML templates for rendering views.
// It aggregates various pieces of data needed for the UI.
type PageData struct {
	Pivot    time.Time // 'Pivot' date determines the central date for data display, e.g., in 7-day summary.
	Summary  []DailySummary
	Food     []FoodEntry
	QuickAdd []QuickAddItem
	Goals    *GoalProjection
}

// WeightLogRequest defines the expected JSON payload for logging weight.
// Date is optional and defaults to the current day if not provided.
type WeightLogRequest struct {
	WeightKg float64 `json:"weight_kg"`
	Date     string  `json:"date,omitempty"` // YYYY-MM-DD, defaults to today
}

// WeightLogResponse is the standard JSON response for the weight logging API.
type WeightLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// CalorieLogRequest defines the expected JSON payload for logging a calorie entry.
// Date is optional and defaults to the current day.
type CalorieLogRequest struct {
	Calories int    `json:"calories"`
	Note     string `json:"note,omitempty"`
	Date     string `json:"date,omitempty"` // YYYY-MM-DD, defaults to today
}

// CalorieLogResponse is the standard JSON response for the calorie logging API.
type CalorieLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// CardioLogRequest defines the expected JSON payload for logging cardio activity.
// Date is optional and defaults to the current day.
type CardioLogRequest struct {
	DurationMin int    `json:"duration_min"`
	Date        string `json:"date,omitempty"` // YYYY-MM-DD, defaults to today
}

// CardioLogResponse is the standard JSON response for the cardio logging API.
type CardioLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// MoodLogRequest defines the expected JSON payload for logging mood.
// Date is optional and defaults to the current day.
type MoodLogRequest struct {
	Mood int    `json:"mood"`
	Date string `json:"date,omitempty"` // YYYY-MM-DD, defaults to today
}

// MoodLogResponse is a generic JSON response structure used by mood logging and other API endpoints for success/failure messages.
type MoodLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// CaloriesTodayResponse defines the JSON response for the API endpoint that retrieves total calories for the current day.
type CaloriesTodayResponse struct {
	Date          string `json:"date"`
	TotalCalories int    `json:"total_calories"`
}

/* ───────────────────── Helpers for templates ───────────────────── */

// FormatNote formats a food log note for display.
// It handles sql.NullString and a specific string pattern.
func FormatNote(note sql.NullString) string {
	if !note.Valid || note.String == "" {
		return "–"
	}

	s := note.String

	// Legacy exports sometimes look like `{Note text true}` or `{Note text false}`.
	// Remove the braces and trailing boolean so only the note text remains.
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		inner := strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
		inner = strings.TrimSuffix(inner, " true")
		inner = strings.TrimSuffix(inner, " false")
		inner = strings.TrimSpace(inner)
		if inner == "" {
			return "–"
		}
		return inner
	}

	return s
}

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

// fmtIntWithSign formats an int pointer to a string with a leading sign if positive, or "–" if nil.
func fmtIntWithSign(p *int) string {
	if p == nil {
		return "–"
	}
	return fmt.Sprintf("%+d", *p)
}

func safeHTML(s string) template.HTML { return template.HTML(s) }
func mod(a, b int) int                { return a % b }
func todayStr() string                { return time.Now().Format("2006-01-02") }
func sub(a, b int) int                { return a - b }
func or(a *int, def int) int {
	if a == nil {
		return def
	}
	return *a
}

/* ───────────────────── Core app ───────────────────── */

type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type App struct {
	db  DB                 // db is the PostgreSQL connection pool or mock.
	tpl *template.Template // tpl stores parsed HTML templates.
}

func main() {
	// Load environment variables from .env file (if present).
	_ = godotenv.Load()

	// Initialize database connection pool.
	pool, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("pgx pool: %v", err)
	}
	defer pool.Close() // Ensure the pool is closed when main exits.

	// Define custom functions for use within HTML templates.
	funcs := template.FuncMap{
		"fmtF2":          fmtF2,          // Formats a float64 pointer to a string with 1 decimal place, or "–" if nil.
		"fmtInt":         fmtInt,         // Formats an int pointer to a string, or "–" if nil.
		"safeHTML":       safeHTML,       // Allows embedding unescaped HTML.
		"mod":            mod,            // Modulo operator for template logic.
		"todayStr":       todayStr,       // Returns current date as "YYYY-MM-DD".
		"formatNote":     FormatNote,     // Formats food log notes.
		"sub":            sub,            // Subtracts two integers.
		"or":             or,             // Returns the first value if not nil, otherwise the second.
		"fmtIntWithSign": fmtIntWithSign, // Formats an int pointer with sign.
	}
	// Parse HTML templates from embedded resources.
	// Includes all .tmpl files in 'views' and 'views/partials'.
	tpl := template.Must(template.New("").Funcs(funcs).ParseFS(
		resources, "views/*.tmpl", "views/partials/*.tmpl"))

	// Create an App instance containing the DB pool and templates.
	app := &App{db: pool, tpl: tpl}

	// Initialize HTTP request multiplexers.
	uiMux := http.NewServeMux()  // Serves UI and API endpoints on the main address.
	apiMux := http.NewServeMux() // API-only server for MCP.

	// Register UI handlers on the main multiplexer.
	uiMux.HandleFunc("/", app.handleIndex)        // Main page, shows daily summary and food log.
	uiMux.HandleFunc("/log", app.handleLog)       // Handles form submissions for daily metrics.
	uiMux.HandleFunc("/food", app.handleFood)     // Handles form submissions for food entries.
	uiMux.HandleFunc("/weekly", app.handleWeekly) // Renders the weekly summary page.

	// Register API endpoints on both multiplexers.
	registerAPIRoutes(uiMux, app)
	registerAPIRoutes(apiMux, app)

	// Serve static assets like compiled CSS on the main server only.
	uiMux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Determine addresses for the regular and MCP servers.
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8181" // default address for regular server
	}

	mcpAddr := os.Getenv("MCP_ADDR") // optional second server

	// Configure the HTTP server used for the main instance.
	server := &http.Server{
		Addr:    addr,
		Handler: uiMux,
	}

	// Configure the MCP server only if an address is provided.
	var mcpServer *http.Server
	if mcpAddr != "" {
		mcpServer = &http.Server{
			Addr:    mcpAddr,
			Handler: apiMux,
		}
	}

	// Start the primary HTTP server in a new goroutine.
	go func() {
		log.Printf("Listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err) // Log fatal error if server fails to start (excluding ErrServerClosed).
		}
	}()

	// Start the MCP server if configured.
	if mcpServer != nil {
		go func() {
			log.Printf("Listening on MCP %s", mcpAddr)
			if err := mcpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("http(mcp): %v", err)
			}
		}()
	}

	// Graceful shutdown setup.
	// Listen for interrupt (Ctrl-C) or SIGTERM signals.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig // Block until a signal is received.

	// Perform shutdown with a timeout.
	log.Println("Shutting down …")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx) // Attempt to gracefully shut down the server.
	if mcpServer != nil {
		_ = mcpServer.Shutdown(ctx)
	}
}

// registerAPIRoutes attaches all API endpoint handlers to the provided mux.
// It is used for both the main server and the API-only MCP server.
func registerAPIRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("/api/bmi", app.handleBMI)
	mux.HandleFunc("/api/log/weight", app.handleLogWeight)
	mux.HandleFunc("/api/log/calorie", app.handleLogCalorie)
	mux.HandleFunc("/api/log/cardio", app.handleLogCardio)
	mux.HandleFunc("/api/log/mood", app.handleLogMood)
	mux.HandleFunc("/api/summary/daily", app.handleGetDailySummary)
	mux.HandleFunc("/api/calories/today", app.handleGetCaloriesToday)
	mux.HandleFunc("/api/food", app.handleGetFood)
	mux.HandleFunc("/api/summary/weekly", app.handleGetWeeklySummary)
}

/* ───────────────────── DB helpers ───────────────────── */

// fetchSummary retrieves a slice of DailySummary objects for a specified date range.
// The range is defined by a 'pivot' date and a 'span' (number of days before and after pivot).
// It queries the `v_daily_summary` view.
// userID is currently hardcoded to 1.
func (a *App) fetchSummary(ctx context.Context, pivot time.Time, span int) ([]DailySummary, error) {
	// Calculate start and end dates for the query.
	start := pivot.AddDate(0, 0, -span)
	end := pivot.AddDate(0, 0, span)

	// SQL query to fetch daily summaries.
	// Note: user_id = 1 is hardcoded, assuming a single-user application.
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
		// Nullable fields from DB need to be checked for validity before assignment.
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
	// rows.Err() checks for errors encountered during iteration.
	return out, rows.Err()
}

// fetchFood retrieves all food entries logged for the current day.
// It queries `daily_calorie_entries` joined with `daily_logs`.
// userID is currently hardcoded to 1.
// Results are ordered by creation time.
func (a *App) fetchFood(ctx context.Context) ([]FoodEntry, error) {
	// SQL query to fetch food entries for user_id 1 and today's date.
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

// fetchQuickAdd retrieves a list of up to 5 most frequently logged food items (note and calories combination).
// This is used to populate the "Quick Add" section in the UI.
// It queries `daily_calorie_entries` and groups by note and calories.
// userID is currently hardcoded to 1.
// Results are ordered by frequency (COUNT(*)) and then by most recent entry (MAX(e.created_at)).
func (a *App) fetchQuickAdd(ctx context.Context) ([]QuickAddItem, error) {
	// SQL query to find common food entries.
	// COALESCE(NULLIF(e.note,''),'') treats NULL or empty notes as empty strings for grouping.
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

// weightTrend calculates the average daily weight change over the last 30 days.
// It returns the most recent weight and the change per day (positive or negative).
// If insufficient data is available, rate will be 0.
func (a *App) weightTrend(ctx context.Context) (current float64, rate float64, err error) {
	rows, err := a.db.Query(ctx, `
                SELECT log_date, weight_kg FROM (
                        SELECT log_date, weight_kg
                          FROM v_daily_summary
                         WHERE user_id = 1 AND weight_kg IS NOT NULL
                         ORDER BY log_date DESC
                         LIMIT 30
                ) t ORDER BY log_date`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()

	type entry struct {
		dt time.Time
		w  float64
	}
	var data []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.dt, &e.w); err != nil {
			return 0, 0, err
		}
		data = append(data, e)
	}
	if len(data) == 0 {
		return 0, 0, nil
	}
	current = data[len(data)-1].w
	if len(data) < 2 {
		return current, 0, nil
	}
	first := data[0]
	days := data[len(data)-1].dt.Sub(first.dt).Hours() / 24
	if days == 0 {
		return current, 0, nil
	}
	rate = (current - first.w) / days
	return current, rate, nil
}

// calculateGoalProjection returns estimated dates to reach milestone and final weight goals.
func (a *App) calculateGoalProjection(ctx context.Context, milestone, goal float64) (*GoalProjection, error) {
	current, dailyRate, err := a.weightTrend(ctx)
	if err != nil {
		return nil, err
	}
	gp := &GoalProjection{
		CurrentWeight:   current,
		DailyChange:     dailyRate,
		MilestoneWeight: milestone,
		GoalWeight:      goal,
	}

	if dailyRate == 0 {
		return gp, nil
	}
	now := time.Now()
	if current > milestone && dailyRate < 0 {
		days := int(math.Ceil((milestone - current) / dailyRate))
		if days >= 0 {
			t := now.Add(time.Duration(days) * 24 * time.Hour)
			gp.MilestoneDays = &days
			gp.MilestoneDate = &t
			gp.MilestoneFormula = fmt.Sprintf("(%.1f - %.1f)/%.3f = %d days", milestone, current, dailyRate, days)

		}
	}
	if current > goal && dailyRate < 0 {
		days := int(math.Ceil((goal - current) / dailyRate))
		if days >= 0 {
			t := now.Add(time.Duration(days) * 24 * time.Hour)
			gp.GoalDays = &days
			gp.GoalDate = &t
			gp.GoalFormula = fmt.Sprintf("(%.1f - %.1f)/%.3f = %d days", goal, current, dailyRate, days)
		}
	}
	return gp, nil
}

// fetchSingleDaySummary retrieves the DailySummary for a specific date and user.
// If no data exists for that date, it returns a DailySummary with only LogDate set,
// and other fields as nil. This is not considered an error (sql.ErrNoRows is handled).
// Any other database error is returned.
func (a *App) fetchSingleDaySummary(ctx context.Context, date time.Time, userID int) (DailySummary, error) {
	var summary DailySummary
	summary.LogDate = date // Initialize LogDate in the summary.

	var (
		weight                         sql.NullFloat64
		est, bud, mood, motiv, act, sl sql.NullInt32
	)

	// Query the v_daily_summary view for the given user and date.
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
			// If no rows are found, it's not an error for this function's purpose.
			// Return the summary with only LogDate populated.
			return summary, nil
		}
		// For any other error, return the error.
		return summary, err
	}

	// Populate the summary struct with data from the database.
	// Handles nullable fields by checking '.Valid'.
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

// handleIndex serves the main page ('/').
// It fetches daily summary data centered around a 'pivot' date (defaulting to today),
// today's food entries, and quick-add food items.
// The 'd' query parameter can be used to set the pivot date (e.g., "?d=2023-01-15").
func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Determine the pivot date for the summary view.
	// Defaults to today, can be overridden by 'd' query parameter.
	pivot := time.Now()
	if qs := r.URL.Query().Get("d"); qs != "" {
		if p, err := time.Parse("2006-01-02", qs); err == nil {
			pivot = p
		}
	}

	// Fetch summary data (e.g., for a 7-day view, span would be 3 days before/after pivot).
	summary, err := a.fetchSummary(ctx, pivot, 3)
	if err != nil {
		log.Printf("Error fetching summary: %v", err)
		http.Error(w, "Error fetching summary data", http.StatusInternalServerError)
		return
	}
	foods, err := a.fetchFood(ctx)
	if err != nil {
		log.Printf("Error fetching food data: %v", err)
		// Decide if you want to show a page with partial data or an error
		// For now, let's return a general error.
		http.Error(w, "Error fetching food data", http.StatusInternalServerError)
		return
	}
	quick, err := a.fetchQuickAdd(ctx)
	if err != nil {
		log.Printf("Error fetching quick add data: %v", err)
		// Decide if you want to show a page with partial data or an error
		http.Error(w, "Error fetching quick add data", http.StatusInternalServerError)
		return
	}

	goals, err := a.calculateGoalProjection(ctx, 63, 60)
	if err != nil {
		log.Printf("Error calculating goals: %v", err)
	}

	// Prepare data for the template.
	data := PageData{
		Pivot:    pivot,
		Summary:  summary,
		Food:     foods,
		QuickAdd: quick,
		Goals:    goals,
	}
	// Render the main index template.
	if err := a.tpl.ExecuteTemplate(w, "index.tmpl", data); err != nil {
		log.Printf("Error executing index.tmpl: %v", err)
		http.Error(w, "Error rendering page", http.StatusInternalServerError)
	}
}

// handleLog handles POST requests to /log for updating various daily metrics like weight, mood, and sleep.
// It uses a helper closure `update` to dynamically construct SQL UPDATE statements based on form values.
// userID is currently hardcoded to 1.
// If the request is not an HTMX request (HX-Request header is not present), it redirects to the home page.
// Otherwise, it returns an HTML partial (summary_partial.tmpl) for HTMX to swap into the page.
func (a *App) handleLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Ensure method is POST.
	if r.Method != http.MethodPost {
		http.Error(w, "method", 405)
		return
	}
	// Parse form data.
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", 400)
		return
	}

	// userID is hardcoded, reflecting single-user design.
	userID := 1
	// Ensure a daily_log entry exists for today, creating one if necessary.
	_, _ = a.db.Exec(ctx, `INSERT INTO daily_logs (user_id, log_date)
	                       VALUES ($1, CURRENT_DATE)
	                       ON CONFLICT (user_id, log_date) DO NOTHING`, userID)

	// Helper closure to update a specific column in daily_logs.
	// This avoids repetitive SQL execution code for different fields.
	update := func(col, formKey string) {
		val := r.FormValue(formKey)
		if val == "" { // Only update if a value is provided in the form.
			return
		}
		// Dynamically construct the SQL query. Be cautious with dynamic SQL;
		// here `col` is from a controlled set of strings, not direct user input.
		_, err := a.db.Exec(ctx, fmt.Sprintf(
			`UPDATE daily_logs SET %s = $1 WHERE user_id = $2 AND log_date = CURRENT_DATE`, col),
			val, userID)
		if err != nil {
			log.Printf("update %s: %v", col, err) // Keep logging
			// If it's an HTMX request, we might want to send back an error that HTMX can process.
			// For now, if an update fails, the subsequent fetchSummary will show the old data.
			// A more robust solution would be to return an error snippet or a specific HTTP status
			// that the frontend HTMX code can interpret as a partial failure.
			// However, the current structure re-fetches and re-renders the summary partial.
			// A simple approach for now is to log and let it re-render.
			// If a specific error response is needed for HTMX, that would be a more involved change.
			// For now, we'll focus on not silently failing.
			// Consider if any error here should halt further updates or return an immediate error response.
			// For simplicity in this step, we will log it. A comprehensive solution might involve
			// collecting errors and returning a summary of issues.
		}
	}
	// Update specific fields based on form values.
	update("weight_kg", "weight_kg")
	update("mood", "mood")
	update("sleep_duration", "sleep_min") // Form key "sleep_min" maps to "sleep_duration" column.

	// HTMX specific handling:
	// If the request does not have the "HX-Request" header, it's a standard form submission.
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/", 303) // Redirect to home page.
		return
	}
	// If it is an HTMX request, render and return only the summary partial.
	sum, _ := a.fetchSummary(ctx, time.Now(), 3) // Fetch fresh summary data.
	if err := a.tpl.ExecuteTemplate(w, "summary_partial.tmpl", sum); err != nil {
		log.Printf("Error executing summary_partial.tmpl: %v", err)
		// For HTMX, we might still want to return an error that HTMX can handle,
		// or at least log it. Depending on HTMX setup, a 500 might be fine.
		http.Error(w, "Error rendering summary partial", http.StatusInternalServerError)
	}
}

// handleFood handles POST and DELETE requests to /food.
// POST logs a new calorie entry, ensuring a `daily_logs` record exists for the
// current day (user_id hardcoded to 1). DELETE removes a calorie entry by ID.
// For HTMX requests, both actions return the updated food list and the summary
// fragment (the latter marked for out-of-band swap). Non-HTMX requests redirect
// to the home page.
func (a *App) handleFood(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}

		// Validate calorie input.
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

		if _, err = a.db.Exec(ctx, `
                        INSERT INTO daily_calorie_entries (log_id, calories, note)
                        VALUES ($1, $2, NULLIF($3,''))`, logID, cal, note); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

	case http.MethodDelete:
		idStr := r.URL.Query().Get("id")
		id, err := strconv.Atoi(idStr)
		if err != nil || id <= 0 {
			http.Error(w, "bad id", 400)
			return
		}
		userID := 1
		if _, err := a.db.Exec(ctx, `
                        DELETE FROM daily_calorie_entries e
                        USING daily_logs l
                        WHERE e.log_id = l.log_id
                          AND l.user_id = $1
                          AND e.entry_id = $2`, userID, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}

	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}

	// HTMX specific handling.
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/", 303)
		return
	}

	// For HTMX, fetch updated food list and summary.
	foods, _ := a.fetchFood(ctx)
	sum, _ := a.fetchSummary(ctx, time.Now(), 3)

	// Render food list and summary partials into string builders.
	var foodHTML, sumHTML strings.Builder
	if err := a.tpl.ExecuteTemplate(&foodHTML, "food.tmpl", foods); err != nil {
		log.Printf("Error executing food.tmpl: %v", err)
		http.Error(w, "Error rendering food entries", http.StatusInternalServerError)
		return
	}
	if err := a.tpl.ExecuteTemplate(&sumHTML, "summary_partial.tmpl", sum); err != nil {
		log.Printf("Error executing summary_partial.tmpl for food handler: %v", err)
		http.Error(w, "Error rendering summary partial", http.StatusInternalServerError)
		return
	}

	// Modify the summary HTML to include an out-of-band swap instruction for HTMX.
	// This tells HTMX to replace the element with id="summary" elsewhere on the page.
	summaryFrag := strings.Replace(sumHTML.String(),
		`id="summary"`, `id="summary" hx-swap-oob="outerHTML"`, 1)

	// Return both fragments to the client.
	fmt.Fprint(w, foodHTML.String(), "\n", summaryFrag)
}

// handleBMI serves GET requests to /api/bmi.
// It returns a JSON array of BMI values for the last 30 days.
// Data is fetched from `v_bmi` view, joined with a generated series of dates
// to ensure all dates in the 30-day period are present, even if no BMI is logged.
// userID is hardcoded to 1.
func (a *App) handleBMI(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// SQL query to get BMI for the last 30 days.
	// generate_series creates a list of dates, LEFT JOIN ensures all dates are included.
	// userID is hardcoded to 1.
	const sql = `
    SELECT d.dt AS log_date, b.bmi AS value
    FROM generate_series(
       CURRENT_DATE - INTERVAL '29 days', -- 30 days including today
       CURRENT_DATE,
       '1 day'
    ) AS d(dt)
    LEFT JOIN v_bmi AS b
      ON b.log_date = d.dt AND b.user_id = $1 -- Hardcoded user_id
    ORDER BY d.dt;
  `
	rows, err := a.db.Query(ctx, sql, 1) // user_id = 1
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	series := make([]BMI, 0, 30)
	for rows.Next() {
		var b BMI
		if err := rows.Scan(&b.LogDate, &b.Value); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		series = append(series, b)
	}

	// Set content type and encode the series as JSON response.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(series)
}

// handleWeekly serves the /weekly page.
// It fetches statistics for the current week from `v_weekly_stats` view
// (hardcoded for userID 1 and the current week) and renders the weekly.tmpl template.
func (a *App) handleWeekly(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var wk Weekly
	// Fetch weekly stats for user_id 1 and the start of the current week.
	err := a.db.QueryRow(ctx, `
		SELECT week_start, avg_weight, total_budgeted, total_estimated, total_deficit
		  FROM v_weekly_stats
		 WHERE user_id = 1
		   AND week_start = date_trunc('week', CURRENT_DATE)`).
		Scan(&wk.WeekStart, &wk.AvgWeight, &wk.TotalBudgeted, &wk.TotalEstimated, &wk.TotalDeficit)
	if err != nil {
		if err == sql.ErrNoRows {
			// wk is already zeroed struct. Set a specific WeekStart if needed,
			// or the template should handle nil/zero values gracefully.
			// We can pass wk as is, and let the template show "no data".
			// Ensure WeekStart is set for the template if it relies on it.
			// If date_trunc query for default week start failed earlier, this part won't be reached.
			// Assuming wk needs a valid WeekStart for the template:
			var currentWeekStart time.Time
			errDateTrunc := a.db.QueryRow(ctx, `SELECT date_trunc('week', CURRENT_DATE);`).Scan(&currentWeekStart)
			if errDateTrunc != nil {
				log.Printf("Error fetching current week start for empty weekly view: %v", errDateTrunc)
				http.Error(w, "Error preparing weekly data", http.StatusInternalServerError)
				return
			}
			wk.WeekStart = currentWeekStart // Set for the template. Other fields will be nil/zero.
			// Log that no data was found, but proceed to render the template.
			log.Printf("No weekly stats found for user_id=1, week_start=%s. Rendering page with no data.", wk.WeekStart.Format("2006-01-02"))
		} else {
			log.Printf("Error fetching weekly stats: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := a.tpl.ExecuteTemplate(w, "weekly.tmpl", wk); err != nil { // Moved from original spot
		log.Printf("Error executing weekly.tmpl: %v", err)
		http.Error(w, "Error rendering weekly page", http.StatusInternalServerError)
	}
}

// handleLogWeight handles POST requests to /api/log/weight.
// It expects a JSON payload with "weight_kg" and an optional "date" (YYYY-MM-DD).
// If date is not provided, it defaults to the current day.
// It ensures a `daily_logs` entry exists for the user (hardcoded as 1) and date,
// then updates the `weight_kg` for that log entry.
// Returns a JSON response indicating success or failure.
func (a *App) handleLogWeight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Ensure method is POST.
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// Decode JSON payload.
	var reqPayload WeightLogRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		log.Printf("Error decoding weight log payload: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}

	// Validate weight value.
	if reqPayload.WeightKg <= 0 {
		log.Printf("Invalid weight_kg value: %f", reqPayload.WeightKg)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "weight_kg must be a positive value"})
		return
	}

	// Determine log date, defaulting to today.
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

	userID := 1 // Hardcoded user_id.

	// Ensure daily_log entry exists and get its ID.
	var logID int
	err := a.db.QueryRow(ctx, `
		INSERT INTO daily_logs (user_id, log_date)
		VALUES ($1, $2)
		ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date -- Ensures log_id is returned
		RETURNING log_id`, userID, logDate).Scan(&logID)

	if err != nil {
		log.Printf("Error upserting daily_log for user %d, date %s: %v", userID, logDate, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Database error while preparing log entry."})
		return
	}

	// Update weight_kg in the daily_logs table.
	// This matches the pattern in `handleLog` where `daily_logs` is updated directly.
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

	// Return success response.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(WeightLogResponse{Success: true, Message: "Weight logged successfully"})
}

// handleLogCalorie handles POST requests to /api/log/calorie.
// Expects JSON payload with "calories", optional "note", and optional "date".
// Defaults to current day if date is not provided.
// Ensures `daily_logs` entry exists for user (hardcoded 1) and date, then inserts
// a new entry into `daily_calorie_entries`.
// Returns JSON response indicating success or failure.
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

	if reqPayload.Calories < 0 { // Calories should not be negative.
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

	userID := 1 // Hardcoded user_id.

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

	// Insert the calorie entry into daily_calorie_entries.
	// NULLIF($3,'') ensures empty note strings are stored as NULL in the database.
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

// handleLogCardio handles POST requests to /api/log/cardio.
// Expects JSON with "duration_min" and optional "date". Defaults to current day.
// Ensures `daily_logs` entry exists, then updates `total_activity_min` by adding the new duration.
// COALESCE is used to handle NULL `total_activity_min` values in the database.
// Returns JSON response indicating success or failure.
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

	if reqPayload.DurationMin < 0 { // Duration should not be negative.
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

	userID := 1 // Hardcoded user_id.

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

	// Update total_activity_min in daily_logs.
	// COALESCE(total_activity_min, 0) ensures that if total_activity_min is NULL, it's treated as 0 before adding.
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

// handleLogMood handles POST requests to /api/log/mood.
// Expects JSON with "mood" (integer) and optional "date". Defaults to current day.
// Ensures `daily_logs` entry exists, then updates the `mood` for that log.
// Returns JSON response indicating success or failure.
func (a *App) handleLogMood(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var reqPayload MoodLogRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		log.Printf("Error decoding mood log payload: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}

	// Optional: Add validation for mood value if a specific range is expected (e.g., 1-5).
	// e.g., if reqPayload.Mood < 1 || reqPayload.Mood > 5 { ... return error ... }

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

	userID := 1 // Hardcoded user_id.

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

	// Update mood in daily_logs.
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

// handleGetDailySummary handles GET requests to /api/summary/daily.
// It expects an optional "date" query parameter (YYYY-MM-DD).
// If "date" is not provided or invalid, it defaults to the current day.
// Fetches daily summary metrics for the specified date and user (hardcoded as 1)
// using `fetchSingleDaySummary`.
// Returns a JSON object of type DailySummary. If no data exists for the date,
// metrics fields in the JSON will be null.
func (a *App) handleGetDailySummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	// Determine the query date from the "date" query parameter.
	dateStr := r.URL.Query().Get("date")
	var queryDate time.Time
	var err error

	if dateStr == "" {
		queryDate = time.Now() // Default to today if no date parameter.
	} else {
		queryDate, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			log.Printf("Invalid date format query parameter: %s, error: %v", dateStr, err)
			http.Error(w, "Invalid date format. Please use YYYY-MM-DD.", http.StatusBadRequest)
			return
		}
	}
	// Normalize queryDate to ensure only YYYY-MM-DD is considered (time part is zeroed).
	queryDate = time.Date(queryDate.Year(), queryDate.Month(), queryDate.Day(), 0, 0, 0, 0, queryDate.Location())

	userID := 1 // Hardcoded user_id.

	// Fetch the summary for the single day.
	summary, err := a.fetchSingleDaySummary(ctx, queryDate, userID)
	if err != nil {
		// `fetchSingleDaySummary` handles sql.ErrNoRows internally.
		// An error here indicates a more significant database issue.
		log.Printf("Error fetching single day summary for user %d, date %s: %v", userID, queryDate.Format("2006-01-02"), err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Error fetching daily summary."})
		return
	}

	// LogDate in summary is already correctly set by fetchSingleDaySummary.
	// If no record was found for the date, metric fields will be nil.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(summary)
}

// handleGetCaloriesToday handles GET requests to /api/calories/today.
// It calculates the total calories logged for the current day for user 1 (hardcoded).
// Returns a JSON response with the current date and total calories.
// COALESCE is used in SQL to ensure 0 is returned if no calorie entries exist.
func (a *App) handleGetCaloriesToday(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	currentDate := time.Now()
	userID := 1 // Hardcoded user_id.

	var totalCalories int
	// Query to sum calories for the user on the current date.
	// COALESCE(SUM(e.calories), 0) ensures 0 if no entries, not NULL.
	err := a.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(e.calories), 0)
		  FROM daily_calorie_entries e
		  JOIN daily_logs dl ON e.log_id = dl.log_id
		 WHERE dl.user_id = $1 AND dl.log_date = $2`,
		userID, currentDate.Format("2006-01-02")).Scan(&totalCalories)

	if err != nil {
		log.Printf("Error fetching total calories for user %d, date %s: %v", userID, currentDate.Format("2006-01-02"), err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
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

// handleGetFood handles GET requests to /api/food.
// It returns all calorie entries for the current day for user 1 as JSON.
func (a *App) handleGetFood(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	entries, err := a.fetchFood(ctx)
	if err != nil {
		log.Printf("Error fetching food entries: %v", err)
		http.Error(w, "Error fetching food entries", http.StatusInternalServerError)
		return
	}

	// Convert to a structure suited for JSON output so sql.NullString becomes *string.
	type apiEntry struct {
		ID        int       `json:"id"`
		CreatedAt time.Time `json:"created_at"`
		Calories  int       `json:"calories"`
		Note      *string   `json:"note,omitempty"`
	}

	out := make([]apiEntry, 0, len(entries))
	for _, e := range entries {
		var note *string
		if e.Note.Valid {
			noteVal := e.Note.String
			note = &noteVal
		}
		out = append(out, apiEntry{
			ID:        e.ID,
			CreatedAt: e.CreatedAt,
			Calories:  e.Calories,
			Note:      note,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleGetWeeklySummary handles GET requests to /api/summary/weekly.
// Expects an optional "start_date" query parameter (YYYY-MM-DD).
// If "start_date" is not provided or invalid, it defaults to the start of the current week.
// The provided "start_date" is normalized to the beginning of its week (e.g., Monday).
// Fetches weekly summary statistics from `v_weekly_stats` for the determined week start date
// and user (hardcoded as 1).
// Returns a JSON object of type Weekly. If no data for the week, metric fields are nil/zero.
func (a *App) handleGetWeeklySummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}

	dateStr := r.URL.Query().Get("start_date")
	var weekStartDate time.Time
	var err error
	userID := 1 // Hardcoded user_id.

	// Determine the week start date.
	if dateStr == "" {
		// Default to the start of the current week (e.g., Monday).
		err = a.db.QueryRow(ctx, `SELECT date_trunc('week', CURRENT_DATE);`).Scan(&weekStartDate)
		if err != nil {
			log.Printf("Error fetching default week start date: %v", err)
			http.Error(w, "Error determining current week start date.", http.StatusInternalServerError)
			return
		}
	} else {
		parsedDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			log.Printf("Invalid start_date format query parameter: %s, error: %v", dateStr, err)
			http.Error(w, "Invalid start_date format. Please use YYYY-MM-DD.", http.StatusBadRequest)
			return
		}
		// Normalize the parsed date to the start of its week.
		var actualWeekStartForProvidedDate time.Time
		// Pass parsedDate directly to query
		err = a.db.QueryRow(ctx, `SELECT date_trunc('week', $1::date);`, parsedDate.Format("2006-01-02")).Scan(&actualWeekStartForProvidedDate)
		if err != nil {
			log.Printf("Error truncating provided start_date %s: %v", parsedDate.Format("2006-01-02"), err)
			http.Error(w, "Error processing provided start_date.", http.StatusInternalServerError)
			return
		}
		weekStartDate = actualWeekStartForProvidedDate
	}

	var weeklySummary Weekly
	// Ensure WeekStart in response is UTC and only date part, for consistency.
	weeklySummary.WeekStart = time.Date(weekStartDate.Year(), weekStartDate.Month(), weekStartDate.Day(), 0, 0, 0, 0, time.UTC)

	// Fetch weekly statistics from the v_weekly_stats view.
	err = a.db.QueryRow(ctx, `
		SELECT avg_weight, total_budgeted, total_estimated, total_deficit
		  FROM v_weekly_stats
		 WHERE user_id = $1 AND week_start = $2`, // user_id = 1, and determined week_start
		userID, weeklySummary.WeekStart).Scan(
		&weeklySummary.AvgWeight,
		&weeklySummary.TotalBudgeted,
		&weeklySummary.TotalEstimated,
		&weeklySummary.TotalDeficit,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			// If no data for the week, return the summary with WeekStart set and metrics as nil/zero.
			// This is considered a valid response, not an error.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(weeklySummary)
			return
		}
		// For other database errors, log and return an internal server error.
		log.Printf("Error fetching weekly summary for user %d, week_start %s: %v", userID, weeklySummary.WeekStart.Format("2006-01-02"), err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Error fetching weekly summary."})
		return
	}

	// Successfully fetched data.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(weeklySummary)
}
