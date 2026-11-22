// Подкоманда enroll: клиентский enrollment против central (ADR-0014).
//
//   - node enroll --config node.yaml --token <token> — выполнить enrollment
//     (если серт жив — ничего не делает) и вывести статус JSON в stdout.
//   - node enroll --config node.yaml --status — только статус серта, без сети.
//
// Electron-хост вызывает обе формы: --status при каждом запуске, --token
// при первом запуске и при пере-enroll (за 7 дней до конца срока).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/enroll"

	"github.com/urfave/cli/v3"
)

func cmdEnroll() *cli.Command {
	return &cli.Command{
		Name:  "enroll",
		Usage: "Enrollment против central: получить/обновить клиентский сертификат",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "путь к YAML-конфигу (как у web)",
			},
			&cli.StringFlag{
				Name:  "token",
				Usage: "enrollment-токен (можно через env ENROLLMENT_CLIENT_TOKEN); пусто — не делать сетевой запрос",
			},
			&cli.BoolFlag{
				Name:  "status",
				Usage: "только показать статус серта (JSON), без сетевого запроса",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := config.Load(cmd.String("config"))
			if err != nil {
				return err
			}
			clientCfg := enroll.ClientConfig{
				CentralURL: cfg.Sync.CentralURL,
				DataDir:    cfg.Node.DataDir,
				CertFile:   cfg.Security.CertFile,
				KeyFile:    cfg.Security.KeyFile,
				CAFile:     cfg.Security.CAFile,
			}
			status := enroll.StatusOf(clientCfg.CertPath(), time.Now())
			token := cmd.String("token")
			if token == "" {
				token = os.Getenv("ENROLLMENT_CLIENT_TOKEN")
			}
			// --status или токен не передан: только диагностика без сети
			// (Electron сам решает, запрашивать ли токен у пользователя).
			if cmd.Bool("status") || token == "" {
				return printStatus(status)
			}
			status, err = enroll.Enroll(ctx, clientCfg, token, time.Now())
			if err != nil && !status.Enrolled {
				return fmt.Errorf("enroll: %w", err)
			}
			return printStatus(status)
		},
	}
}

// printStatus пишет статус JSON в stdout — контракт для Electron-хоста.
func printStatus(st enroll.Status) error {
	enc := json.NewEncoder(os.Stdout)
	return enc.Encode(st)
}
