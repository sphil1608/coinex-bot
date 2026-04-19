package engine

import (
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/rs/zerolog/log"
)

// StartDashboard spins up a lightweight REST API for monitoring.
// Endpoints:
//   GET /signals      – recent signal log
//   GET /orders       – open orders
//   GET /orderbook    – current order book snapshot
//   GET /health       – liveness check
func (e *Engine) StartDashboard(port int) {
	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})
	app.Use(cors.New())

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	app.Get("/signals", func(c *fiber.Ctx) error {
		sigs := e.GetSignalLog()
		b, _ := json.Marshal(sigs)
		c.Set("Content-Type", "application/json")
		return c.Send(b)
	})

	app.Get("/orders", func(c *fiber.Ctx) error {
		orders := e.GetOpenOrders()
		b, _ := json.Marshal(orders)
		c.Set("Content-Type", "application/json")
		return c.Send(b)
	})

	app.Get("/orderbook", func(c *fiber.Ctx) error {
		ob := e.lob.Snapshot()
		b, _ := json.Marshal(ob)
		c.Set("Content-Type", "application/json")
		return c.Send(b)
	})

	addr := fmt.Sprintf(":%d", port)
	log.Info().Str("addr", addr).Msg("dashboard listening")
	if err := app.Listen(addr); err != nil {
		log.Error().Err(err).Msg("dashboard error")
	}
}
