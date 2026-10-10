package middleware

import "github.com/gofiber/fiber/v2/middleware/limiter"

func RateLimiter() limiter.Config { return limiter.Config{Max: 100} }
