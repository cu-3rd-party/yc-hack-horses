// Команда node: веб-сервер ноды (по умолчанию) и миграции.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/cu3rd/yc-hack-horses/backend/internal/app"
	"github.com/cu3rd/yc-hack-horses/backend/internal/config"

	"github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		Name:  "node",
		Usage: "yc-hack-horses node",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "путь к YAML-конфигу (значения по умолчанию — как в config.example.yaml)",
			},
		},
		Commands: []*cli.Command{
			cmdWeb(),
			cmdEnroll(),
			cmdMigrate(),
		},
		DefaultCommand: "web",
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		slog.Error("node exited with error", "err", err)
		os.Exit(1)
	}
}

func cmdWeb() *cli.Command {
	return &cli.Command{
		Name:  "web",
		Usage: "Запустить REST/WS сервер ноды",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load(cmd.String("config"))
			if err != nil {
				return err
			}
			initLogging(cfg.LogLevel)
			a, err := app.New(cfg)
			if err != nil {
				return err
			}
			// graceful shutdown по SIGINT/SIGTERM
			ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return a.Run(ctx)
		},
	}
}

func cmdMigrate() *cli.Command {
	return &cli.Command{
		Name:  "migrate",
		Usage: "Применить миграции схемы и выйти",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load(cmd.String("config"))
			if err != nil {
				return err
			}
			initLogging(cfg.LogLevel)
			return app.Migrate(cfg)
		},
	}
}

func initLogging(level string) {
	var l slog.Level
	switch level {
	case "debug", "DEBUG":
		l = slog.LevelDebug
	case "warn", "WARN":
		l = slog.LevelWarn
	case "error", "ERROR":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: l})))
}
