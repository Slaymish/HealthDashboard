# HealthDashboard

HealthDashboard is a personal web application designed for tracking various health and wellness metrics. It provides a user-friendly interface to log and visualize data related to weight, calorie intake, mood, sleep, and physical activity.

## Features

*   **Daily Logging:** Record daily weight, estimated and budgeted calories, mood, motivation, activity duration, and sleep duration.
*   **Food Entry:** Log individual food items with calorie counts and notes for the current day.
*   **Quick Add Food:** Quickly re-add frequently logged food items.
*   **Visualizations & Summaries:**
    *   View a daily summary of all logged metrics.
    *   See a 7-day overview of key metrics.
    *   Display a 30-day BMI trend chart.
    *   Get a weekly summary including average weight, total calories (estimated vs. budgeted), and calorie deficit.
*   **API Endpoints:** Provides a JSON API for logging data (weight, calories, cardio, mood) and retrieving summaries (daily, weekly, today's calories).

## Getting Started

### Prerequisites

*   **Go:** Version 1.19 or higher (refer to `go.mod` for specific dependencies).
*   **PostgreSQL:** A running PostgreSQL instance is required.
*   **Python (Optional):** Python 3.x and `pandas` library are needed if you intend to use the `logs.py` script for data import.

### Setup & Running

1.  **Clone the repository:**
    ```bash
    git clone <your-repo-url>
    cd HealthDashboard
    ```

2.  **Configure Environment Variables:**
    *   Copy the environment variable template:
        ```bash
        cp .env.template .env
        ```
    *   Edit the `.env` file and set your `DATABASE_URL`:
        ```
        DATABASE_URL=postgres://youruser:yourpassword@yourhost:yourport/yourdatabase
        ```

3.  **Database Schema:**
    *   The application expects a certain database schema to be in place. The schema is not managed by this application (e.g., no automated migrations). You will need to create the tables and views manually. Key tables and views suggested by the application code include:
        *   `daily_logs` (stores daily metrics like weight, mood, sleep)
        *   `daily_calorie_entries` (stores individual food/calorie entries linked to `daily_logs`)
        *   `v_daily_summary` (a view summarizing daily data)
        *   `v_bmi` (a view for calculating BMI over time)
        *   `v_weekly_stats` (a view for calculating weekly statistics)
    *   Refer to the data structures in `main.go` (e.g., `DailySummary`, `FoodEntry`) and the SQL queries for insights into the required schema.

4.  **Build and Run the Go Application:**
    *   From the root of the project directory:
        ```bash
        go build
        ./HealthDashboard
        ```
    *   The application will start, and by default, listen on port `:8181`.

## Python Import Script (`logs.py`)

The `logs.py` script is provided as a utility to process and convert data from a CSV file (seemingly exported from an app called "Cut Tracker") into a format that might be easier to import into the HealthDashboard database.

### Usage

1.  **Install Dependencies:**
    ```bash
    pip install pandas
    ```
2.  **Prepare your data:** Ensure your source CSV file has columns like "Day", "Weight", "Budgeted kcal", etc., as expected by the script (see `header_to_field` in `logs.py`).
3.  **Run the script:**
    The script has been modified to accept input and output file paths as command-line arguments.
    ```bash
    python logs.py <input_csv_path> <output_csv_path>
    ```
    For example:
    ```bash
    python logs.py "/path/to/your/Cut Tracker - Cut tracker.csv" "/tmp/sanitized_health_logs.csv"
    ```
    This will create `sanitized_health_logs.csv` with the processed data.
4.  **Import to Database:** The generated CSV can then be imported into your PostgreSQL database using tools like `COPY` command in `psql` or a database management tool.

**Note:** The original script had hardcoded input and output paths. This has been changed for better flexibility. You will need to adapt the import process to your specific database setup.

## Known Limitations & Future Considerations

*   **Single User Focus:** The application currently hardcodes `userID = 1` in many database queries. This means it's designed for a single user. Supporting multiple users would require significant changes to authentication, data separation, and API design.
*   **Code Organization:** All Go backend code is in `main.go`. As the project grows, consider splitting this into multiple files/packages (e.g., `handlers.go`, `models.go`, `db.go`) for better maintainability.
*   **Database Migrations:** There is no system for managing database schema changes or migrations. Schema setup is manual. Implementing a migration tool (e.g., `golang-migrate/migrate`) would be beneficial for future development.
*   **Data Import Process:** The current CSV import process via `logs.py` is manual and somewhat fragile. For more robust data integration, consider:
    *   Building dedicated API endpoints for bulk data import.
    *   Developing a more integrated data loading tool or service.
*   **Error Handling:** While basic error handling is in place, more structured logging (e.g., using a library like `logrus` or `zap`) and more user-friendly error feedback on the frontend could be implemented.
*   **Configuration:** Currently, only `DATABASE_URL` is configurable via `.env`. Other parameters like the server port are hardcoded. These could also be moved to environment variables.

This README provides a starting point. Feel free to expand it with more details about your specific setup or future plans!
