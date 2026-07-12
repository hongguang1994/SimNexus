package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// SlogLogger is a Gin middleware that logs each HTTP request via slog (feeds the log buffer).
func SlogLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		status := c.Writer.Status()
		lvl := slog.LevelInfo
		if status >= 500 {
			lvl = slog.LevelError
		} else if status >= 400 {
			lvl = slog.LevelWarn
		}
		slog.Log(c.Request.Context(), lvl, c.Request.Method+" "+c.Request.URL.Path,
			"cat", "http",
			"status", status,
			"latency", latency.Round(time.Millisecond).String(),
			"ip", c.ClientIP(),
		)
	}
}
