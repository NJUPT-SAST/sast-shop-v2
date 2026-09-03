package v1

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v5"
)

const HealthReadyPath = "/health/ready"

type ReadyChecker interface {
	Ready(context.Context) error
}

type readinessHandler struct {
	checker ReadyChecker
}

func (h readinessHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeReadinessError(w, http.StatusMethodNotAllowed, "SERVICE_METHOD_NOT_ALLOWED")
		return
	}
	if h.checker == nil || h.checker.Ready(r.Context()) != nil {
		writeReadinessError(w, http.StatusServiceUnavailable, "SERVICE_NOT_READY")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(struct {
		Status string `json:"status"`
	}{Status: "ok"})
}

func writeReadinessError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{Code: code})
}

func RegisterHealthReady(e *echo.Echo, checker ReadyChecker) {
	e.GET(HealthReadyPath, echo.WrapHandler(readinessHandler{checker: checker}))
}
