package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

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
		respondErr(w, http.StatusInternalServerError, "Error fetching summary data", err)
		return
	}
	foods, err := a.fetchFood(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "Error fetching food data", err)
		return
	}
	quick, err := a.fetchQuickAdd(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "Error fetching quick add data", err)
		return
	}
	goals, err := a.calculateGoalProjection(ctx, 63, 60)
	if err != nil {
		logger.Error("calculate goals", "err", err)
	}
	data := PageData{
		Pivot:    pivot,
		Summary:  summary,
		Food:     foods,
		QuickAdd: quick,
		Goals:    goals,
	}
	if err := a.tpl.ExecuteTemplate(w, "index.tmpl", data); err != nil {
		respondErr(w, http.StatusInternalServerError, "Error rendering page", err)
	}
}

func (a *App) handleLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
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
		_, err := a.db.Exec(ctx, fmt.Sprintf(`UPDATE daily_logs SET %s = $1 WHERE user_id = $2 AND log_date = CURRENT_DATE`, col), val, userID)
		if err != nil {
			logger.Error("update", "column", col, "err", err)
		}
	}
	update("weight_kg", "weight")
	update("mood", "mood")
	update("sleep_duration", "sleep")
	update("motivation", "motivation")
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	sum, _ := a.fetchSummary(ctx, time.Now(), 3)
	var out strings.Builder
	if err := a.tpl.ExecuteTemplate(&out, "summary_partial.tmpl", sum); err != nil {
		respondErr(w, http.StatusInternalServerError, "Error rendering", err)
		return
	}
	fmt.Fprint(w, out.String())
}

func (a *App) handleFood(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.Method {
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		cal, err := strconv.Atoi(r.FormValue("calories"))
		if err != nil || cal < 0 {
			http.Error(w, "calories", http.StatusBadRequest)
			return
		}
		note := r.FormValue("note")
		userID := 1
		var logID int
		if err := a.db.QueryRow(ctx, `
                        INSERT INTO daily_logs (user_id, log_date)
                        VALUES ($1, CURRENT_DATE)
                        ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date
                        RETURNING log_id`, userID).Scan(&logID); err != nil {
			respondErr(w, http.StatusInternalServerError, "Database error", err)
			return
		}
		if _, err = a.db.Exec(ctx, `
                        INSERT INTO daily_calorie_entries (log_id, calories, note)
                        VALUES ($1, $2, NULLIF($3,''))`, logID, cal, note); err != nil {
			respondErr(w, http.StatusInternalServerError, "Database error", err)
			return
		}
	case http.MethodDelete:
		id, err := strconv.Atoi(r.URL.Query().Get("id"))
		if err != nil || id <= 0 {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		userID := 1
		if _, err := a.db.Exec(ctx, `
                        DELETE FROM daily_calorie_entries e
                        USING daily_logs l
                        WHERE e.log_id = l.log_id
                          AND l.user_id = $1
                          AND e.entry_id = $2`, userID, id); err != nil {
			respondErr(w, http.StatusInternalServerError, "Database error", err)
			return
		}
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("HX-Request") == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	foods, _ := a.fetchFood(ctx)
	sum, _ := a.fetchSummary(ctx, time.Now(), 3)
	var foodHTML, sumHTML strings.Builder
	if err := a.tpl.ExecuteTemplate(&foodHTML, "food.tmpl", foods); err != nil {
		respondErr(w, http.StatusInternalServerError, "Error rendering food entries", err)
		return
	}
	if err := a.tpl.ExecuteTemplate(&sumHTML, "summary_partial.tmpl", sum); err != nil {
		respondErr(w, http.StatusInternalServerError, "Error rendering summary partial", err)
		return
	}
	summaryFrag := strings.Replace(sumHTML.String(), `id="summary"`, `id="summary" hx-swap-oob="outerHTML"`, 1)
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
    ORDER BY d.dt;`
	rows, err := a.db.Query(ctx, sql, 1)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "Database error", err)
		return
	}
	defer rows.Close()
	series := make([]BMI, 0, 30)
	for rows.Next() {
		var b BMI
		if err := rows.Scan(&b.LogDate, &b.Value); err != nil {
			respondErr(w, http.StatusInternalServerError, "Database error", err)
			return
		}
		series = append(series, b)
	}
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
		if err == sql.ErrNoRows {
			var currentWeekStart time.Time
			errDateTrunc := a.db.QueryRow(ctx, `SELECT date_trunc('week', CURRENT_DATE);`).Scan(&currentWeekStart)
			if errDateTrunc != nil {
				respondErr(w, http.StatusInternalServerError, "Error preparing weekly data", errDateTrunc)
				return
			}
			wk.WeekStart = currentWeekStart
			logger.Info("no weekly stats", "week_start", wk.WeekStart.Format("2006-01-02"))
		} else {
			respondErr(w, http.StatusInternalServerError, "Error fetching weekly stats", err)
			return
		}
	}
	if err := a.tpl.ExecuteTemplate(w, "weekly.tmpl", wk); err != nil {
		respondErr(w, http.StatusInternalServerError, "Error rendering weekly page", err)
	}
}

func (a *App) handleLogWeight(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	var reqPayload WeightLogRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		logger.Error("decode weight payload", "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}
	if reqPayload.WeightKg <= 0 {
		logger.Error("invalid weight_kg", "value", reqPayload.WeightKg)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "weight_kg must be a positive value"})
		return
	}
	logDate := time.Now().Format("2006-01-02")
	if reqPayload.Date != "" {
		parsedDate, err := time.Parse("2006-01-02", reqPayload.Date)
		if err != nil {
			logger.Error("invalid date", "date", reqPayload.Date, "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Invalid date format. Please use YYYY-MM-DD."})
			return
		}
		logDate = parsedDate.Format("2006-01-02")
	}
	userID := 1
	var logID int
	err := a.db.QueryRow(ctx, `
                INSERT INTO daily_logs (user_id, log_date)
                VALUES ($1, $2)
                ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date
                RETURNING log_id`, userID, logDate).Scan(&logID)
	if err != nil {
		logger.Error("upsert daily_log", "user", userID, "date", logDate, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Database error while preparing log entry."})
		return
	}
	_, err = a.db.Exec(ctx,
		`UPDATE daily_logs SET weight_kg = $1 WHERE log_id = $2 AND user_id = $3`,
		reqPayload.WeightKg, logID, userID)
	if err != nil {
		logger.Error("update weight", "log_id", logID, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(WeightLogResponse{Success: false, Message: "Database error while updating weight."})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(WeightLogResponse{Success: true, Message: "Weight logged successfully"})
}

func (a *App) handleLogCalorie(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	var reqPayload CalorieLogRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		logger.Error("decode calorie payload", "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}
	if reqPayload.Calories < 0 {
		logger.Error("invalid calories", "value", reqPayload.Calories)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "calories must be a non-negative value"})
		return
	}
	logDate := time.Now().Format("2006-01-02")
	if reqPayload.Date != "" {
		parsedDate, err := time.Parse("2006-01-02", reqPayload.Date)
		if err != nil {
			logger.Error("invalid date", "date", reqPayload.Date, "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "Invalid date format. Please use YYYY-MM-DD."})
			return
		}
		logDate = parsedDate.Format("2006-01-02")
	}
	userID := 1
	var logID int
	err := a.db.QueryRow(ctx, `
                INSERT INTO daily_logs (user_id, log_date)
                VALUES ($1, $2)
                ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date
                RETURNING log_id`, userID, logDate).Scan(&logID)
	if err != nil {
		logger.Error("upsert daily_log", "user", userID, "date", logDate, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "Database error while preparing log entry."})
		return
	}
	_, err = a.db.Exec(ctx, `
                INSERT INTO daily_calorie_entries (log_id, calories, note)
                VALUES ($1, $2, NULLIF($3,''))`, logID, reqPayload.Calories, reqPayload.Note)
	if err != nil {
		logger.Error("insert calorie", "log_id", logID, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CalorieLogResponse{Success: false, Message: "Database error while logging calorie entry."})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(CalorieLogResponse{Success: true, Message: "Calorie entry logged successfully"})
}

func (a *App) handleLogCardio(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	var reqPayload CardioLogRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		logger.Error("decode cardio payload", "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}
	if reqPayload.DurationMin < 0 {
		logger.Error("invalid duration", "value", reqPayload.DurationMin)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "duration_min must be a non-negative value"})
		return
	}
	logDate := time.Now().Format("2006-01-02")
	if reqPayload.Date != "" {
		parsedDate, err := time.Parse("2006-01-02", reqPayload.Date)
		if err != nil {
			logger.Error("invalid date", "date", reqPayload.Date, "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "Invalid date format. Please use YYYY-MM-DD."})
			return
		}
		logDate = parsedDate.Format("2006-01-02")
	}
	userID := 1
	var logID int
	err := a.db.QueryRow(ctx, `
                INSERT INTO daily_logs (user_id, log_date)
                VALUES ($1, $2)
                ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date
                RETURNING log_id`, userID, logDate).Scan(&logID)
	if err != nil {
		logger.Error("upsert daily_log", "user", userID, "date", logDate, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "Database error while preparing log entry."})
		return
	}
	_, err = a.db.Exec(ctx,
		`UPDATE daily_logs
                SET total_activity_min = COALESCE(total_activity_min, 0) + $1
                WHERE log_id = $2 AND user_id = $3`,
		reqPayload.DurationMin, logID, userID)
	if err != nil {
		logger.Error("update activity", "log_id", logID, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(CardioLogResponse{Success: false, Message: "Database error while logging cardio activity."})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(CardioLogResponse{Success: true, Message: "Cardio activity logged successfully"})
}

func (a *App) handleLogMood(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}
	var reqPayload MoodLogRequest
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		logger.Error("decode mood payload", "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Invalid JSON payload: " + err.Error()})
		return
	}
	logDate := time.Now().Format("2006-01-02")
	if reqPayload.Date != "" {
		parsedDate, err := time.Parse("2006-01-02", reqPayload.Date)
		if err != nil {
			logger.Error("invalid date", "date", reqPayload.Date, "err", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Invalid date format. Please use YYYY-MM-DD."})
			return
		}
		logDate = parsedDate.Format("2006-01-02")
	}
	userID := 1
	var logID int
	err := a.db.QueryRow(ctx, `
                INSERT INTO daily_logs (user_id, log_date)
                VALUES ($1, $2)
                ON CONFLICT (user_id, log_date) DO UPDATE SET log_date = EXCLUDED.log_date
                RETURNING log_id`, userID, logDate).Scan(&logID)
	if err != nil {
		logger.Error("upsert daily_log", "user", userID, "date", logDate, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Database error while preparing log entry."})
		return
	}
	_, err = a.db.Exec(ctx,
		`UPDATE daily_logs SET mood = $1 WHERE log_id = $2 AND user_id = $3`,
		reqPayload.Mood, logID, userID)
	if err != nil {
		logger.Error("update mood", "log_id", logID, "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Database error while logging mood."})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(MoodLogResponse{Success: true, Message: "Mood logged successfully"})
}

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
			logger.Error("invalid date query", "date", dateStr, "err", err)
			http.Error(w, "Invalid date format. Please use YYYY-MM-DD.", http.StatusBadRequest)
			return
		}
	}
	queryDate = time.Date(queryDate.Year(), queryDate.Month(), queryDate.Day(), 0, 0, 0, 0, queryDate.Location())
	userID := 1
	summary, err := a.fetchSingleDaySummary(ctx, queryDate, userID)
	if err != nil {
		logger.Error("fetch single day summary", "user", userID, "date", queryDate.Format("2006-01-02"), "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(MoodLogResponse{Success: false, Message: "Error fetching daily summary."})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(summary)
}

func (a *App) handleGetCaloriesToday(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}
	currentDate := time.Now()
	userID := 1
	var totalCalories int
	err := a.db.QueryRow(ctx, `
                SELECT COALESCE(SUM(e.calories), 0)
                  FROM daily_calorie_entries e
                  JOIN daily_logs dl ON e.log_id = dl.log_id
                 WHERE dl.user_id = $1 AND dl.log_date = $2`,
		userID, currentDate.Format("2006-01-02")).Scan(&totalCalories)
	if err != nil {
		logger.Error("fetch total calories", "user", userID, "date", currentDate.Format("2006-01-02"), "err", err)
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

func (a *App) handleGetFood(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}
	entries, err := a.fetchFood(ctx)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "Error fetching food entries", err)
		return
	}
	type apiEntry struct {
		ID        int       `json:"id"`
		CreatedAt time.Time `json:"created_at"`
		Calories  int       `json:"calories"`
		Note      *string   `json:"note,omitempty"`
	}
	var out []apiEntry
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

func (a *App) handleGetWeeklySummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		http.Error(w, "Only GET method is allowed", http.StatusMethodNotAllowed)
		return
	}
	dateStr := r.URL.Query().Get("start_date")
	var weekStartDate time.Time
	var err error
	userID := 1
	if dateStr == "" {
		err = a.db.QueryRow(ctx, `SELECT date_trunc('week', CURRENT_DATE);`).Scan(&weekStartDate)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "Error determining current week start date", err)
			return
		}
	} else {
		parsedDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			logger.Error("invalid start_date", "date", dateStr, "err", err)
			http.Error(w, "Invalid start_date format. Please use YYYY-MM-DD.", http.StatusBadRequest)
			return
		}
		var actualWeekStartForProvidedDate time.Time
		err = a.db.QueryRow(ctx, `SELECT date_trunc('week', $1::date);`, parsedDate.Format("2006-01-02")).Scan(&actualWeekStartForProvidedDate)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "Error processing provided start_date", err)
			return
		}
		weekStartDate = actualWeekStartForProvidedDate
	}
	var weeklySummary Weekly
	weeklySummary.WeekStart = time.Date(weekStartDate.Year(), weekStartDate.Month(), weekStartDate.Day(), 0, 0, 0, 0, time.UTC)
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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(weeklySummary)
			return
		}
		respondErr(w, http.StatusInternalServerError, "Error fetching weekly summary", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(weeklySummary)
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data := PageData{}
		_ = a.tpl.ExecuteTemplate(w, "login.tmpl", data)
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.FormValue("pin") == "1234" {
			http.SetCookie(w, &http.Cookie{Name: "pin", Value: "1234", Path: "/", Expires: time.Now().Add(365 * 24 * time.Hour), HttpOnly: true})
			http.Redirect(w, r, "/", http.StatusSeeOther)
		} else {
			data := PageData{Error: "Invalid PIN"}
			_ = a.tpl.ExecuteTemplate(w, "login.tmpl", data)
		}
	default:
		http.Error(w, "method", http.StatusMethodNotAllowed)
	}
}
