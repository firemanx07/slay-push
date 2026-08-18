package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// HealthHandler reports liveness plus Postgres/Redis connectivity.
func HealthHandler(db *pgxpool.Pool, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		status := struct {
			Status   string `json:"status"`
			Database string `json:"database"`
			Redis    string `json:"redis"`
		}{Status: "ok", Database: "ok", Redis: "ok"}

		httpStatus := http.StatusOK

		if err := db.Ping(ctx); err != nil {
			status.Database = err.Error()
			status.Status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}
		if err := rdb.Ping(ctx).Err(); err != nil {
			status.Redis = err.Error()
			status.Status = "degraded"
			httpStatus = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		_ = json.NewEncoder(w).Encode(status)
	}
}
