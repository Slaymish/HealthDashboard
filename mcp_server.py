from __future__ import annotations

import os
import json
import asyncpg
from dotenv import load_dotenv
from starlette.requests import Request
from starlette.responses import JSONResponse, Response
from mcp.server.fastmcp import FastMCP
import contextlib

pool: asyncpg.pool.Pool | None = None

@contextlib.asynccontextmanager
async def lifespan(server: FastMCP):
    global pool
    load_dotenv()
    database_url = os.environ.get("DATABASE_URL")
    if not database_url:
        raise RuntimeError("DATABASE_URL environment variable is required")
    pool = await asyncpg.create_pool(database_url)
    try:
        yield
    finally:
        await pool.close()

server = FastMCP(lifespan=lifespan)

# Utility function to get a connection
async def get_conn():
    if pool is None:
        raise RuntimeError("Database pool not initialized")
    return pool.acquire()

USER_ID = 1  # single-user assumption as in Go code

@server.custom_route("/api/bmi", methods=["GET"])
async def api_bmi(request: Request) -> Response:
    async with pool.acquire() as conn:
        rows = await conn.fetch(
            """
            SELECT d.dt AS log_date, b.bmi AS value
            FROM generate_series(CURRENT_DATE - INTERVAL '29 days', CURRENT_DATE, '1 day') AS d(dt)
            LEFT JOIN v_bmi AS b ON b.log_date = d.dt AND b.user_id = $1
            ORDER BY d.dt
            """,
            USER_ID,
        )
    result = [
        {"date": row["log_date"].isoformat(), "bmi": row["value"]}
        for row in rows
    ]
    return JSONResponse(result)

@server.custom_route("/api/log/weight", methods=["POST"])
async def api_log_weight(request: Request) -> Response:
    data = await request.json()
    weight = data.get("weight_kg")
    if weight is None or weight <= 0:
        return JSONResponse({"success": False, "message": "weight_kg must be positive"}, status_code=400)
    date_str = data.get("date")
    if date_str:
        try:
            log_date = date_str
            # asyncpg can parse string to date directly
        except Exception:
            return JSONResponse({"success": False, "message": "Invalid date format. Use YYYY-MM-DD."}, status_code=400)
    else:
        log_date = None

    async with pool.acquire() as conn:
        if log_date:
            row = await conn.fetchrow(
                "INSERT INTO daily_logs (user_id, log_date) VALUES ($1, $2) "
                "ON CONFLICT (user_id, log_date) DO UPDATE SET log_date=EXCLUDED.log_date RETURNING log_id",
                USER_ID,
                log_date,
            )
        else:
            row = await conn.fetchrow(
                "INSERT INTO daily_logs (user_id, log_date) VALUES ($1, CURRENT_DATE) "
                "ON CONFLICT (user_id, log_date) DO UPDATE SET log_date=EXCLUDED.log_date RETURNING log_id",
                USER_ID,
            )
        log_id = row["log_id"]
        await conn.execute(
            "UPDATE daily_logs SET weight_kg=$1 WHERE log_id=$2 AND user_id=$3",
            weight,
            log_id,
            USER_ID,
        )
    return JSONResponse({"success": True, "message": "Weight logged successfully"})

@server.custom_route("/api/log/calorie", methods=["POST"])
async def api_log_calorie(request: Request) -> Response:
    data = await request.json()
    calories = data.get("calories")
    if calories is None or calories < 0:
        return JSONResponse({"success": False, "message": "calories must be non-negative"}, status_code=400)
    note = data.get("note")
    date_str = data.get("date")
    async with pool.acquire() as conn:
        if date_str:
            row = await conn.fetchrow(
                "INSERT INTO daily_logs (user_id, log_date) VALUES ($1, $2) "
                "ON CONFLICT (user_id, log_date) DO UPDATE SET log_date=EXCLUDED.log_date RETURNING log_id",
                USER_ID,
                date_str,
            )
        else:
            row = await conn.fetchrow(
                "INSERT INTO daily_logs (user_id, log_date) VALUES ($1, CURRENT_DATE) "
                "ON CONFLICT (user_id, log_date) DO UPDATE SET log_date=EXCLUDED.log_date RETURNING log_id",
                USER_ID,
            )
        log_id = row["log_id"]
        await conn.execute(
            "INSERT INTO daily_calorie_entries (log_id, calories, note) VALUES ($1, $2, NULLIF($3,''))",
            log_id,
            calories,
            note or "",
        )
    return JSONResponse({"success": True, "message": "Calorie entry logged successfully"})

@server.custom_route("/api/log/cardio", methods=["POST"])
async def api_log_cardio(request: Request) -> Response:
    data = await request.json()
    duration = data.get("duration_min")
    if duration is None or duration < 0:
        return JSONResponse({"success": False, "message": "duration_min must be non-negative"}, status_code=400)
    date_str = data.get("date")
    async with pool.acquire() as conn:
        if date_str:
            row = await conn.fetchrow(
                "INSERT INTO daily_logs (user_id, log_date) VALUES ($1, $2) "
                "ON CONFLICT (user_id, log_date) DO UPDATE SET log_date=EXCLUDED.log_date RETURNING log_id",
                USER_ID,
                date_str,
            )
        else:
            row = await conn.fetchrow(
                "INSERT INTO daily_logs (user_id, log_date) VALUES ($1, CURRENT_DATE) "
                "ON CONFLICT (user_id, log_date) DO UPDATE SET log_date=EXCLUDED.log_date RETURNING log_id",
                USER_ID,
            )
        log_id = row["log_id"]
        await conn.execute(
            "UPDATE daily_logs SET total_activity_min = COALESCE(total_activity_min, 0) + $1 "
            "WHERE log_id=$2 AND user_id=$3",
            duration,
            log_id,
            USER_ID,
        )
    return JSONResponse({"success": True, "message": "Cardio activity logged successfully"})

@server.custom_route("/api/log/mood", methods=["POST"])
async def api_log_mood(request: Request) -> Response:
    data = await request.json()
    mood = data.get("mood")
    if mood is None:
        return JSONResponse({"success": False, "message": "mood is required"}, status_code=400)
    date_str = data.get("date")
    async with pool.acquire() as conn:
        if date_str:
            row = await conn.fetchrow(
                "INSERT INTO daily_logs (user_id, log_date) VALUES ($1, $2) "
                "ON CONFLICT (user_id, log_date) DO UPDATE SET log_date=EXCLUDED.log_date RETURNING log_id",
                USER_ID,
                date_str,
            )
        else:
            row = await conn.fetchrow(
                "INSERT INTO daily_logs (user_id, log_date) VALUES ($1, CURRENT_DATE) "
                "ON CONFLICT (user_id, log_date) DO UPDATE SET log_date=EXCLUDED.log_date RETURNING log_id",
                USER_ID,
            )
        log_id = row["log_id"]
        await conn.execute(
            "UPDATE daily_logs SET mood=$1 WHERE log_id=$2 AND user_id=$3",
            mood,
            log_id,
            USER_ID,
        )
    return JSONResponse({"success": True, "message": "Mood logged successfully"})

@server.custom_route("/api/summary/daily", methods=["GET"])
async def api_summary_daily(request: Request) -> Response:
    date_str = request.query_params.get("date")
    if date_str:
        query_date = date_str
    else:
        query_date = None
    async with pool.acquire() as conn:
        if query_date:
            row = await conn.fetchrow(
                """
                SELECT weight_kg, kcal_estimated, kcal_budgeted, mood, motivation, total_activity_min, sleep_duration
                FROM v_daily_summary
                WHERE user_id=$1 AND log_date=$2
                """,
                USER_ID,
                query_date,
            )
            log_date = query_date
        else:
            row = await conn.fetchrow(
                """
                SELECT weight_kg, kcal_estimated, kcal_budgeted, mood, motivation, total_activity_min, sleep_duration, CURRENT_DATE as log_date
                FROM v_daily_summary
                WHERE user_id=$1 AND log_date=CURRENT_DATE
                """,
                USER_ID,
            )
            log_date = None
    if row:
        result = {
            "log_date": (log_date or row.get("log_date")).isoformat() if isinstance(row.get("log_date"), (str, bytes)) else str(row.get("log_date")),
            "weight_kg": row.get("weight_kg"),
            "kcal_estimated": row.get("kcal_estimated"),
            "kcal_budgeted": row.get("kcal_budgeted"),
            "mood": row.get("mood"),
            "motivation": row.get("motivation"),
            "total_activity_min": row.get("total_activity_min"),
            "sleep_duration": row.get("sleep_duration"),
        }
    else:
        result = {"log_date": date_str or None}
    return JSONResponse(result)

@server.custom_route("/api/calories/today", methods=["GET"])
async def api_calories_today(request: Request) -> Response:
    async with pool.acquire() as conn:
        row = await conn.fetchrow(
            """
            SELECT COALESCE(SUM(e.calories),0) as total
            FROM daily_calorie_entries e
            JOIN daily_logs dl ON e.log_id=dl.log_id
            WHERE dl.user_id=$1 AND dl.log_date=CURRENT_DATE
            """,
            USER_ID,
        )
    total = row["total"] if row else 0
    result = {"date": str(await conn.fetchval("SELECT CURRENT_DATE")), "total_calories": total}
    return JSONResponse(result)

@server.custom_route("/api/food", methods=["GET"])
async def api_food(request: Request) -> Response:
    async with pool.acquire() as conn:
        rows = await conn.fetch(
            """
            SELECT e.entry_id, e.created_at, e.calories, e.note
            FROM daily_calorie_entries e
            JOIN daily_logs l ON l.log_id=e.log_id
            WHERE l.user_id=$1 AND l.log_date=CURRENT_DATE
            ORDER BY e.created_at
            """,
            USER_ID,
        )
    out = []
    for row in rows:
        note = row["note"]
        out.append(
            {
                "id": row["entry_id"],
                "created_at": row["created_at"].isoformat(),
                "calories": row["calories"],
                **({"note": note} if note is not None else {}),
            }
        )
    return JSONResponse(out)

@server.custom_route("/api/summary/weekly", methods=["GET"])
async def api_summary_weekly(request: Request) -> Response:
    date_str = request.query_params.get("start_date")
    async with pool.acquire() as conn:
        if date_str:
            week_start = await conn.fetchval("SELECT date_trunc('week', $1::date)", date_str)
        else:
            week_start = await conn.fetchval("SELECT date_trunc('week', CURRENT_DATE)")
        row = await conn.fetchrow(
            """
            SELECT avg_weight, total_budgeted, total_estimated, total_deficit
            FROM v_weekly_stats
            WHERE user_id=$1 AND week_start=$2
            """,
            USER_ID,
            week_start,
        )
    result = {
        "week_start": week_start.isoformat() if week_start else None,
        "avg_weight": row.get("avg_weight") if row else None,
        "total_budgeted": row.get("total_budgeted") if row else None,
        "total_estimated": row.get("total_estimated") if row else None,
        "total_deficit": row.get("total_deficit") if row else None,
    }
    return JSONResponse(result)

if __name__ == "__main__":
    server.run("streamable-http")
