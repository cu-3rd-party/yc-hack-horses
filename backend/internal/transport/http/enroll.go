// HTTP-эндпоинт enrollment: POST /api/v1/enroll (только central, только при
// включённом enrollment — иначе 404, как Scalar-документация).
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/enroll"
)

// enrollRequest — тело запроса.
type enrollRequest struct {
	NodeID string `json:"node_id"`
	CSR    string `json:"csr"`
	Token  string `json:"token"`
}

// enrollResponse — тело ответа: leaf, CA (для удобства клиента) и срок.
type enrollResponse struct {
	NodeCert  string `json:"node_cert"`
	CACert    string `json:"ca_cert"`
	NodeID    string `json:"node_id"`
	ExpiresAt string `json:"expires_at"`
}

// handleEnroll обрабатывает POST /api/v1/enroll.
func handleEnroll(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Enrollment == nil {
			// Enrollment выключен (локальные ноды, central без конфига):
			// эндпоинта как будто нет.
			notFoundJSON(w, r)
			return
		}
		var req enrollRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "некорректное тело запроса")
			return
		}
		nodeCert, caCert, expires, err := d.Enrollment.Enroll(req.NodeID, req.CSR, req.Token)
		if err != nil {
			switch {
			case errors.Is(err, enroll.ErrBadToken):
				slog.Debug("enroll: неверный токен", "node_id", req.NodeID)
				writeError(w, http.StatusForbidden, "неверный enrollment-токен")
			case errors.Is(err, enroll.ErrBadCSR):
				writeError(w, http.StatusBadRequest, err.Error())
			default:
				slog.Error("enroll: внутренняя ошибка", "err", err)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
			return
		}
		writeJSON(w, http.StatusOK, enrollResponse{
			NodeCert:  string(nodeCert),
			CACert:    string(caCert),
			NodeID:    req.NodeID,
			ExpiresAt: expires.Format(time.RFC3339),
		})
	}
}
