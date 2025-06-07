package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func TestHandleLogWeightSuccess(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	// Expect insertion of log and update of weight
	mock.ExpectQuery("INSERT INTO daily_logs").
		WithArgs(1, pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"log_id"}).AddRow(1))
	mock.ExpectExec("UPDATE daily_logs SET weight_kg").
		WithArgs(70.0, 1, 1).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	app := &App{db: mock}

	reqBody := bytes.NewBufferString(`{"weight_kg":70}`)
	req := httptest.NewRequest(http.MethodPost, "/api/log/weight", reqBody)
	w := httptest.NewRecorder()

	app.handleLogWeight(w, req)

	res := w.Result()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out WeightLogResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	require.True(t, out.Success)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHandleLogWeightInvalidJSON(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	app := &App{db: mock}
	reqBody := bytes.NewBufferString(`{"weight_kg":}`)
	req := httptest.NewRequest(http.MethodPost, "/api/log/weight", reqBody)
	w := httptest.NewRecorder()

	app.handleLogWeight(w, req)

	require.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFetchSingleDaySummary(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	dt := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery("FROM v_daily_summary").
		WithArgs(1, dt).
		WillReturnRows(pgxmock.NewRows([]string{
			"weight_kg", "kcal_estimated", "kcal_budgeted",
			"mood", "motivation", "total_activity_min", "sleep_duration",
		}).AddRow(70.5, 2000, 1800, 3, 4, 60, 480))

	app := &App{db: mock}

	sum, err := app.fetchSingleDaySummary(context.Background(), dt, 1)
	require.NoError(t, err)
	require.NotNil(t, sum.WeightKg)
	require.Equal(t, 70.5, *sum.WeightKg)
	require.NotNil(t, sum.KcalEstimated)
	require.Equal(t, 2000, *sum.KcalEstimated)

	require.NoError(t, mock.ExpectationsWereMet())
}
