package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

// Data structs moved from main.go

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

type FoodEntry struct {
	ID        int
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
	Value   *float64  `json:"bmi"`
}

type Weekly struct {
	WeekStart      time.Time `json:"week_start"`
	AvgWeight      *float64  `json:"avg_weight,omitempty"`
	TotalEstimated *int      `json:"total_estimated,omitempty"`
	TotalBudgeted  *int      `json:"total_budgeted,omitempty"`
	TotalDeficit   *int      `json:"total_deficit,omitempty"`
}

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

type PageData struct {
	Pivot     time.Time
	Summary   []DailySummary
	Food      []FoodEntry
	QuickAdd  []QuickAddItem
	Goals     *GoalProjection
	ShowLogin bool
	Error     string
}

type WeightLogRequest struct {
	WeightKg float64 `json:"weight_kg"`
	Date     string  `json:"date,omitempty"`
}

type WeightLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type CalorieLogRequest struct {
	Calories int    `json:"calories"`
	Note     string `json:"note,omitempty"`
	Date     string `json:"date,omitempty"`
}

type CalorieLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type CardioLogRequest struct {
	DurationMin int    `json:"duration_min"`
	Date        string `json:"date,omitempty"`
}

type CardioLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type MoodLogRequest struct {
	Mood int    `json:"mood"`
	Date string `json:"date,omitempty"`
}

type MoodLogResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type CaloriesTodayResponse struct {
	Date          string `json:"date"`
	TotalCalories int    `json:"total_calories"`
}

// Database helper functions moved from main.go

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

func (a *App) fetchSingleDaySummary(ctx context.Context, date time.Time, userID int) (DailySummary, error) {
	var summary DailySummary
	summary.LogDate = date
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
			return summary, nil
		}
		return summary, err
	}
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
