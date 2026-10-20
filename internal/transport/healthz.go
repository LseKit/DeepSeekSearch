// 文件名: healthz.go
// 用途: 提供 /healthz 探活端点，字段与 Contract 15 §七之三逐字一致。
// 创建日期: 2026-10-21
// 作者: 小逸
// 项目: DeepSeekSearch <https://github.com/LseKit/DeepSeekSearch>

package transport

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/LseKit/DeepSeekSearch/internal/config"
	"github.com/LseKit/DeepSeekSearch/internal/domain"
	"github.com/LseKit/DeepSeekSearch/internal/logging"
)

type healthzCache struct {
	Entries  int   `json:"entries"`
	Bytes    int64 `json:"bytes"`
	Degraded bool  `json:"degraded"`
}

type healthzUpstream struct {
	Configured bool   `json:"configured"`
	BaseURL    string `json:"base_url"`
}

type healthzPayload struct {
	Status    string          `json:"status"`
	Name      string          `json:"name"`
	Version   string          `json:"version"`
	Commit    string          `json:"commit"`
	BuildTime string          `json:"build_time"`
	GoVersion string          `json:"go_version"`
	Protocol  string          `json:"protocol"`
	Transport string          `json:"transport"`
	UptimeS   int64           `json:"uptime_s"`
	Cache     healthzCache    `json:"cache"`
	Upstream  healthzUpstream `json:"upstream"`
}

func healthzHandler(cfg *config.Config, build domain.BuildInfo, startedAt time.Time,
	log *slog.Logger, next http.Handler) http.Handler {
	return &middleware{name: "healthz", next: next, serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != cfg.Server.Http.HealthzPath {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		uptime := int64(0)
		if !startedAt.IsZero() {
			uptime = int64(time.Since(startedAt).Seconds())
		}
		payload := healthzPayload{
			Status:    "ok",
			Name:      cfg.App.Name,
			Version:   build.Version,
			Commit:    build.Commit,
			BuildTime: build.BuildTime,
			GoVersion: build.GoVersion,
			Protocol:  "2026-07-28",
			Transport: "streamable-http",
			UptimeS:   uptime,
			// transport 契约没有注入 cache 的通道（Build 只有 cfg/build/logger），
			// 这两个字段先保持零值；探活只承诺「有响应即活着」。
			Cache:    healthzCache{},
			Upstream: healthzUpstream{Configured: cfg.ApiKey != "", BaseURL: cfg.Upstream.BaseURL},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(raw); err != nil {
			log.Warn(logging.EvErr, "stage", "write_healthz_response", "error", err)
		}
	})}
}
