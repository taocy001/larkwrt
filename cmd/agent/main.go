package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"larkwrt/internal/collector"
	"larkwrt/internal/commands"
	"larkwrt/internal/config"
	"larkwrt/internal/events"
	"larkwrt/internal/executor"
	"larkwrt/internal/feishu"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/larkwrt/config.toml", "path to config file")
	debug := flag.Bool("debug", false, "enable debug logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// ── Logger ────────────────────────────────────────────────────────────────
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: "15:04:05"})
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if *debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
	log.Info().Str("version", version).Msg("larkwrt-agent starting")

	// ── Config ────────────────────────────────────────────────────────────────
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatal().Err(err).Msg("load config")
	}

	// ── Core components ───────────────────────────────────────────────────────
	bus := events.NewBus(cfg.Alert.CooldownSecs)
	dbPath := filepath.Join(filepath.Dir(*cfgPath), "devices.json")
	devDB := collector.NewDevDB(dbPath)
	col := collector.New(
		cfg.Monitor.CollectFast.Duration,
		cfg.Monitor.CollectSlow.Duration,
		bus,
		cfg.Router.LanIface,
		devDB,
	)
	shell := executor.New(cfg.Security.ExecWhitelist)
	client := feishu.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret, cfg.Feishu.ChatID, "")
	ws := feishu.NewWSClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret, "")
	router := commands.NewRouter(cfg)

	cmdCtx := commands.Context{
		Client:    client,
		Collector: col,
		DevDB:     devDB,
		Executor:  shell,
		Config:    cfg,
	}

	// ── Event → alert card ────────────────────────────────────────────────────
	bus.Subscribe(func(ev events.Event) {
		card := feishu.BuildAlertCard(cfg.Router.Name, ev)
		if _, err := client.SendCard(card); err != nil {
			log.Error().Err(err).Str("event", ev.Type.String()).Msg("send alert")
		}
	})

	// ── WS event dispatch ─────────────────────────────────────────────────────
	go func() {
		for env := range ws.Events() {
			log.Info().Str("event_type", env.Header.EventType).Str("event_id", env.Header.EventID).Msg("ws event")
			switch env.Header.EventType {
			case "im.message.receive_v1":
				go router.HandleMessage(env, cmdCtx)
			case "card.action.trigger":
				go router.HandleCardAction(env, cmdCtx)
			default:
				log.Warn().Str("type", env.Header.EventType).Msg("unhandled event type")
			}
		}
	}()

	// ── Startup ───────────────────────────────────────────────────────────────
	stop := make(chan struct{})
	go col.Start(stop)
	go ws.Run()

	// send boot notification after collector warms up
	go func() {
		time.Sleep(3 * time.Second)
		card := feishu.BuildStatusCard(cfg.Router.Name, col.Current())
		if _, err := client.SendCard(card); err != nil {
			log.Error().Err(err).Msg("boot notification failed")
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig

	log.Info().Msg("shutting down")
	close(stop)
	ws.Stop()
}
