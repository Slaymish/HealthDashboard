import pandas as pd, datetime as dt, re, pathlib

SRC  = pathlib.Path("/home/hamish/Downloads/Cut Tracker - Cut tracker.csv")
DEST = pathlib.Path("/tmp/sanitized_logs.csv")

start_date = dt.date(2025, 4, 8)      #  Day 1  →  8 Apr 2025

header_to_field = {
    "Weight":              "weight_kg_txt",
    "Budgeted kcal":       "kcal_budgeted_txt",
    "Estimated kcal":      "kcal_estimated_txt",
    "Exercise":            "exercise_txt",
    "Fasted Cardio":       "fasted_cardio_txt",
    "Mood/Energy":         "mood_txt",
    "Notes":               "notes",
}

df = pd.read_csv(SRC)

day_re = re.compile(r"Day\s+(\d+)", re.I)
df = df[df["Day"].astype(str).str.match(day_re)].copy()

# Extract N and compute log_date vector-wise
df["day_num"]  = df["Day"].str.extract(day_re).astype(int)
df["log_date"] = pd.to_datetime(start_date) + pd.to_timedelta(df["day_num"] - 1, unit="d")

# Keep only mapped columns that actually exist
mapped_cols = {new: df[old] for old,new in header_to_field.items() if old in df.columns}
out = pd.concat([df["log_date"], pd.DataFrame(mapped_cols)], axis=1)

out.to_csv(DEST, index=False)
print(f"✅  Wrote {DEST} with {len(out)} rows")

