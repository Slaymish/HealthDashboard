package main

import "net/http"

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
