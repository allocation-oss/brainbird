package middleware

import "github.com/gofiber/fiber/v2/middleware/cors"

func CORS() cors.Config {
	return cors.Config{AllowOrigins: "*", AllowHeaders: "Origin, Content-Type, Accept, Authorization, X-Tenant-ID"}
}
