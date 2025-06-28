package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"os/signal"
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
		logger.Error("pgx pool", "err", err)
		os.Exit(1)
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
	uiMux.HandleFunc("/login", app.handleLogin)   // PIN login page.
	uiMux.HandleFunc("/", app.handleIndex)        // Main page, shows daily summary and food log.
	uiMux.HandleFunc("/log", app.handleLog)       // Handles form submissions for daily metrics.
	uiMux.HandleFunc("/food", app.handleFood)     // Handles form submissions for food entries.
	uiMux.HandleFunc("/weekly", app.handleWeekly) // Renders the weekly summary page.
	uiMux.HandleFunc("/agent", app.handleAgent) // handle the text agent
	uiMux.HandleFunc("/agent/message",app.handleAgentMessage)

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
		Handler: pinAuthMiddleware(uiMux),
	}

	// Configure the MCP server only if an address is provided.
	var mcpServer *http.Server
	if mcpAddr != "" {
		mcpServer = &http.Server{
			Addr:    mcpAddr,
			Handler: pinAuthMiddleware(apiMux),
		}
	}

	// Start the primary HTTP server in a new goroutine.
	go func() {
		logger.Info("server start", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http", "err", err)
			os.Exit(1)
		}
	}()

	// Start the MCP server if configured.
	if mcpServer != nil {
		go func() {
			logger.Info("mcp start", "addr", mcpAddr)
			if err := mcpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("http(mcp)", "err", err)
				os.Exit(1)
			}
		}()
	}

	// Graceful shutdown setup.
	// Listen for interrupt (Ctrl-C) or SIGTERM signals.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig // Block until a signal is received.

	// Perform shutdown with a timeout.
	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx) // Attempt to gracefully shut down the server.
	if mcpServer != nil {
		_ = mcpServer.Shutdown(ctx)
	}
}
