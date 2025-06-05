# HealthDashboard

HealthDashboard is a personal web application designed for tracking various health and wellness metrics. It provides a user-friendly interface to log and visualize data related to weight, calorie intake, mood, sleep, and physical activity.

## Features

*   **Daily Logging:** Record daily weight, estimated and budgeted calories, mood, motivation, activity duration, and sleep duration.
*   **Food Entry:** Log individual food items with calorie counts and notes for the current day.
*   **Delete Entries:** Remove mistaken food logs directly from the table.
*   **Quick Add Food:** Quickly re-add frequently logged food items.
*   **Dark Mode:** Switch between light and dark themes via the header toggle.
*   **Visualizations & Summaries:**
    *   View a daily summary of all logged metrics.
    *   See a 7-day overview of key metrics.
    *   Display a 30-day BMI trend chart.
    *   Get a weekly summary including average weight, total calories (estimated vs. budgeted), and calorie deficit.

## API Endpoints

This section details the available API endpoints for interacting with the HealthDashboard programmatically.

### `GET /api/bmi`

*   **Description:** Retrieves Body Mass Index (BMI) data for the last 30 days.
*   **Request Parameters:** None.
*   **Example JSON Response:**
    ```json
    [
        {
            "date": "2023-10-01T00:00:00Z",
            "bmi": 22.5
        },
        {
            "date": "2023-10-02T00:00:00Z",
            "bmi": null // Indicates no BMI data for this date
        }
        // ... more entries up to 30 days
    ]
    ```

### `POST /api/log/weight`

*   **Description:** Logs weight for a specific date.
*   **Request Body (JSON):**
    ```json
    {
        "weight_kg": 70.5,
        "date": "2023-10-28" // Optional, defaults to today (YYYY-MM-DD)
    }
    ```
*   **Example JSON Response (Success):**
    ```json
    {
        "success": true,
        "message": "Weight logged successfully"
    }
    ```
*   **Example JSON Response (Error):**
    ```json
    {
        "success": false,
        "message": "weight_kg must be a positive value" // Or other error messages
    }
    ```

### `POST /api/log/calorie`

*   **Description:** Logs a calorie entry for a specific date.
*   **Request Body (JSON):**
    ```json
    {
        "calories": 500,
        "note": "Lunch - Salad", // Optional
        "date": "2023-10-28"    // Optional, defaults to today (YYYY-MM-DD)
    }
    ```
*   **Example JSON Response (Success):**
    ```json
    {
        "success": true,
        "message": "Calorie entry logged successfully"
    }
    ```

### `DELETE /food`

*   **Description:** Removes a previously logged food entry.
*   **Request Parameters (query):**
    *   `id` (integer, required): The entry ID to delete.
*   **Response:** For HTMX requests, returns updated HTML fragments. Non-HTMX requests redirect to `/`.

### `POST /api/log/cardio`

*   **Description:** Logs cardio activity duration for a specific date. The duration is added to any existing activity for that day.
*   **Request Body (JSON):**
    ```json
    {
        "duration_min": 30,
        "date": "2023-10-28" // Optional, defaults to today (YYYY-MM-DD)
    }
    ```
*   **Example JSON Response (Success):**
    ```json
    {
        "success": true,
        "message": "Cardio activity logged successfully"
    }
    ```

### `POST /api/log/mood`

*   **Description:** Logs mood for a specific date.
*   **Request Body (JSON):**
    ```json
    {
        "mood": 5, // Typically an integer representing mood level
        "date": "2023-10-28" // Optional, defaults to today (YYYY-MM-DD)
    }
    ```
*   **Example JSON Response (Success):**
    ```json
    {
        "success": true,
        "message": "Mood logged successfully"
    }
    ```

### `GET /api/summary/daily`

*   **Description:** Retrieves a daily summary of all metrics for a specific date.
*   **Request Parameters:**
    *   `date` (string, optional, format: YYYY-MM-DD): The date for which to retrieve the summary. Defaults to the current day if not provided.
*   **Example JSON Response (Data exists):**
    ```json
    {
        "log_date": "2023-10-28T00:00:00Z",
        "weight_kg": 70.5,
        "kcal_estimated": 2000,
        "kcal_budgeted": 1800,
        "mood": 4,
        "motivation": 5,
        "total_activity_min": 45,
        "sleep_duration": 480 // in minutes
    }
    ```
*   **Example JSON Response (No data for the date):**
    ```json
    {
        "log_date": "2023-10-29T00:00:00Z",
        "weight_kg": null,
        "kcal_estimated": null,
        "kcal_budgeted": null,
        "mood": null,
        "motivation": null,
        "total_activity_min": null,
        "sleep_duration": null
    }
    ```

### `GET /api/calories/today`

*   **Description:** Retrieves the total calories logged for the current day.
*   **Request Parameters:** None.
*   **Example JSON Response:**
    ```json
    {
        "date": "2023-10-28",
        "total_calories": 1500
    }
    ```

### `GET /api/summary/weekly`

*   **Description:** Retrieves a weekly summary of key statistics.
*   **Request Parameters:**
    *   `start_date` (string, optional, format: YYYY-MM-DD): The start date of the week for which to retrieve the summary. The API will normalize this to the actual start of that week (e.g., Monday). Defaults to the start of the current week if not provided.
*   **Example JSON Response (Data exists):**
    ```json
    {
        "week_start": "2023-10-23T00:00:00Z", // Week start date (e.g., Monday)
        "avg_weight": 70.2,
        "total_estimated": 14000,
        "total_budgeted": 13500,
        "total_deficit": -500
    }
    ```
*   **Example JSON Response (No data for the week):**
    ```json
    {
        "week_start": "2023-10-30T00:00:00Z",
        "avg_weight": null,
        "total_estimated": null,
        "total_budgeted": null,
        "total_deficit": null
    }
    ```

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

4.  **Install Node Dependencies & Build CSS:**
    *   Tailwind CSS is compiled locally using npm:
        ```bash
        npm install
        npm run build:css
        ```
    *   This generates `static/css/app.css` which is served by the Go app.

5.  **Build and Run the Go Application:**
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
