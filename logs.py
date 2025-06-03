import pandas as pd
import datetime as dt
import re
import pathlib
import argparse

start_date = dt.date(2025, 4, 8)

header_to_field = {
    "Weight":              "weight_kg_txt",
    "Budgeted kcal":       "kcal_budgeted_txt",
    "Estimated kcal":      "kcal_estimated_txt",
    "Exercise":            "exercise_txt",
    "Fasted Cardio":       "fasted_cardio_txt",
    "Mood/Energy":         "mood_txt",
    "Notes":               "notes",
}

def main():
    parser = argparse.ArgumentParser(
        description="Clean and transform health tracking data from a CSV file.",
        epilog="Example: python logs.py input.csv output.csv"
    )
    parser.add_argument("input_file", type=str, help="Path to the input CSV file.")
    parser.add_argument("output_file", type=str, help="Path to save the sanitized output CSV file.")
    args = parser.parse_args()

    input_path = pathlib.Path(args.input_file)
    output_path = pathlib.Path(args.output_file)

    if not input_path.is_file(): # Check if it's a file
        print(f"❌ Error: Input file not found or is not a file: {input_path}")
        return

    print(f"🔄 Processing {input_path}...")

    try:
        df = pd.read_csv(input_path)
    except Exception as e:
        print(f"❌ Error reading CSV file: {e}")
        return

    # Corrected regex: use \s+ for whitespace and \d+ for digits
    day_re = re.compile(r"Day\s+(\d+)", re.I)

    if "Day" not in df.columns:
        print("❌ Error: 'Day' column not found in the input CSV.")
        # Optionally, create an empty output file or exit
        # For now, let's create an empty dataframe to signify no processable data
        out_df = pd.DataFrame()
    else:
        df_filtered = df[df["Day"].astype(str).str.contains(day_re, na=False)].copy() # Use contains and na=False

        if df_filtered.empty:
            print("⚠️ Warning: No rows matched the 'Day N' format (e.g., 'Day 123'). Output might be empty or only headers.")
            out_df = pd.DataFrame() # Prepare an empty DataFrame
        else:
            # Extract N and compute log_date vector-wise
            df_filtered.loc[:, "day_num"] = df_filtered["Day"].str.extract(day_re, expand=False).astype(int)
            df_filtered.loc[:, "log_date"] = pd.to_datetime(start_date) + pd.to_timedelta(df_filtered["day_num"] - 1, unit="d")

            out_data = {}
            out_data["log_date"] = df_filtered["log_date"]

            for old_col, new_col_name in header_to_field.items():
                if old_col in df_filtered.columns:
                    out_data[new_col_name] = df_filtered[old_col]
                else:
                    print(f"🔍 Note: Column '{old_col}' (for '{new_col_name}') not found. It will be skipped.")

            out_df = pd.DataFrame(out_data)
            # Reorder columns
            cols = ["log_date"] + [col for col in out_df.columns if col != "log_date"]
            out_df = out_df[cols]

    try:
        output_path.parent.mkdir(parents=True, exist_ok=True)
        out_df.to_csv(output_path, index=False)
        if out_df.empty and "Day" not in df.columns:
             print(f"⚠️  Wrote {output_path}, but it's empty as 'Day' column was missing or no data matched.")
        elif out_df.empty:
            print(f"⚠️  Wrote {output_path}, but it's empty as no rows matched the 'Day N' format.")
        else:
            print(f"✅ Wrote {output_path} with {len(out_df)} rows")
    except Exception as e:
        print(f"❌ Error writing output file: {e}")

if __name__ == "__main__":
    main()
