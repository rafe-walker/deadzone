// Command deadzone answers "can I take this video call from an off-grid
// homestead?" by fusing Solar Assistant (power), Starlink (uplink) and
// Open-Meteo (weather) and publishing the verdict over MQTT and HTTP.
//
//	deadzone run     [--config deadzone.yaml]   daemon
//	deadzone check   [--config deadzone.yaml]   one-shot verdict as JSON; exit 0 go / 1 maybe / 2 no
//	deadzone version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/rafe-walker/deadzone/internal/actions"
	"github.com/rafe-walker/deadzone/internal/api"
	"github.com/rafe-walker/deadzone/internal/app"
	"github.com/rafe-walker/deadzone/internal/bus"
	"github.com/rafe-walker/deadzone/internal/config"
	"github.com/rafe-walker/deadzone/internal/engine"
	"github.com/rafe-walker/deadzone/internal/source"
	"github.com/rafe-walker/deadzone/internal/source/openmeteo"
	"github.com/rafe-walker/deadzone/internal/source/solarassistant"
	"github.com/rafe-walker/deadzone/internal/source/starlink"
)

// Set by -ldflags "-X main.version=..." at release time.
var (
	version = "0.2.0"
	commit  = ""
	date    = ""
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var code int
	var err error
	switch os.Args[1] {
	case "run":
		err = runCmd(os.Args[2:])
	case "check":
		code, err = checkCmd(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("deadzone %s", version)
		if commit != "" {
			fmt.Printf(" (%s", commit)
			if date != "" {
				fmt.Printf(", %s", date)
			}
			fmt.Print(")")
		}
		fmt.Printf(" %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "deadzone:", err)
		os.Exit(3)
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprint(os.Stderr, `deadzone — can I take this video call from an off-grid homestead?

usage:
  deadzone run     [--config FILE] [--log-level LEVEL] [--advisory]
  deadzone check   [--config FILE] [--wait DURATION] [--pretty=false]
  deadzone version

run    starts the daemon: subscribes to Solar Assistant over MQTT, polls the
       Starlink dish and Open-Meteo, publishes deadzone/* (+ HA discovery) and
       serves the status page on http.listen (default :8099).
check  connects, waits up to --wait for data, prints one State JSON and exits
       0 for green (go), 1 for amber (maybe), 2 for red (no), 3 on error.

Config: YAML file (see deadzone.example.yaml) overridden by DEADZONE_* env vars.
`)
}

type common struct {
	cfg config.Config
	log *slog.Logger
}

func loadCommon(fs *flag.FlagSet, args []string) (common, error) {
	cfgPath := fs.String("config", envOr("DEADZONE_CONFIG", ""), "YAML config file (optional; env overrides apply)")
	level := fs.String("log-level", "", "debug|info|warn|error (overrides config)")
	if err := fs.Parse(args); err != nil {
		return common{}, err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return common{}, err
	}
	if *level != "" {
		cfg.LogLevel = *level
	}
	return common{cfg: cfg, log: newLogger(cfg.LogLevel)}, nil
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn", "warning":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

// stack holds everything run and check share.
type stack struct {
	cfg     config.Config
	log     *slog.Logger
	bus     *bus.Client
	sa      *solarassistant.Source
	sl      *starlink.Source
	om      *openmeteo.Source
	app     *app.App
	sources []source.Source
}

func build(c common, advisory bool) *stack {
	cfg := c.cfg
	s := &stack{cfg: cfg, log: c.log}
	s.bus = bus.New(cfg.MQTT, version, c.log.With("component", "mqtt"))
	s.sa = solarassistant.New(s.bus, solarassistant.Options{
		Prefix:                    cfg.MQTT.SAPrefix,
		StaleAfter:                cfg.StaleAfter(),
		CapacityWh:                cfg.Battery.CapacityKWh * 1000,
		ReserveSOCPct:             cfg.Battery.ReserveSOCPct,
		BatteryPositiveIsCharging: cfg.MQTT.BatteryPositiveIsCharging,
	})
	s.sources = append(s.sources, s.sa)

	var uplink source.Source
	if cfg.Starlink.Enabled {
		caller := starlink.NewReflectCaller(cfg.Starlink.Addr, time.Duration(cfg.Starlink.TimeoutS*float64(time.Second)))
		s.sl = starlink.New(caller, starlink.Options{
			StatusInterval:      time.Duration(cfg.Starlink.StatusIntervalS * float64(time.Second)),
			HistoryInterval:     time.Duration(cfg.Starlink.HistoryIntervalS * float64(time.Second)),
			DiagnosticsInterval: time.Duration(cfg.Starlink.HistoryIntervalS * float64(time.Second)),
			StaleAfter:          cfg.StaleAfter(),
			Logger:              c.log.With("component", "starlink"),
		})
		uplink = s.sl
		s.sources = append(s.sources, s.sl)
	}
	var weather source.Source
	var pr app.PRSink
	if cfg.Forecast.Enabled {
		s.om = openmeteo.New(openmeteo.Options{
			BaseURL:    cfg.Forecast.BaseURL,
			Latitude:   cfg.Forecast.Latitude,
			Longitude:  cfg.Forecast.Longitude,
			TiltDeg:    cfg.Forecast.TiltDeg,
			AzimuthDeg: cfg.Forecast.AzimuthDeg,
			ArrayKWp:   cfg.Forecast.ArrayKWp,
			PR:         cfg.Forecast.PRDefault,
			Refresh:    time.Duration(cfg.Forecast.RefreshMinutes) * time.Minute,
			CachePath:  cfg.Forecast.CachePath,
			Logger:     c.log.With("component", "openmeteo"),
		})
		weather, pr = s.om, s.om
		s.sources = append(s.sources, s.om)
	}
	s.app = app.New(cfg, version, s.sa, uplink, weather, pr, actions.NewAdvisory(advisory), c.log)
	return s
}

func (s *stack) start(ctx context.Context) {
	for _, src := range s.sources {
		if err := src.Start(ctx); err != nil {
			s.log.Error("source failed to start", "source", src.Name(), "err", err)
		}
	}
}

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	advisory := fs.Bool("advisory", true, "record shed recommendations in dry-run mode (no writes happen in v0.1 regardless)")
	every := fs.Duration("interval", 5*time.Second, "evaluation/publish interval")
	c, err := loadCommon(fs, args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	s := build(c, *advisory)
	s.log.Info("deadzone starting", "version", version, "broker", c.cfg.MQTT.BrokerURL(), "sa_prefix", c.cfg.MQTT.SAPrefix,
		"starlink", c.cfg.Starlink.Enabled, "forecast", c.cfg.Forecast.Enabled, "http", c.cfg.HTTP.Listen)

	// Subscriptions are registered before Connect so they apply on the
	// first connect and every reconnect.
	if err := s.sa.Start(ctx); err != nil {
		return err
	}
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if err := s.bus.Connect(connectCtx); err != nil {
		s.log.Warn("mqtt not connected yet; will keep retrying", "err", err)
	}
	cancel()
	defer s.bus.Close()

	for _, src := range s.sources {
		if src == s.sa {
			continue
		}
		if err := src.Start(ctx); err != nil {
			s.log.Error("source failed to start", "source", src.Name(), "err", err)
		}
	}

	deps := api.Deps{State: s.app.State, Version: version}
	if s.sl != nil {
		deps.History = s.sl.History15m
	}
	httpErr := make(chan error, 1)
	go func() {
		httpErr <- api.Serve(ctx, c.cfg.HTTP.Listen, api.NewHandler(deps), s.log.With("component", "http"))
	}()

	t := time.NewTicker(*every)
	defer t.Stop()
	publish := func() {
		st := s.app.Evaluate(ctx)
		if !s.bus.Connected() {
			s.log.Debug("evaluated; mqtt offline", "call_ok", st.Verdict.CallOK)
			return
		}
		if err := s.bus.PublishState(st); err != nil {
			s.log.Warn("publish failed", "err", err)
			return
		}
		s.log.Debug("published", "call_ok", st.Verdict.CallOK, "hours", st.Verdict.HoursWorkstation, "score", st.Verdict.UplinkScore)
	}
	publish()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("shutting down")
			return nil
		case err := <-httpErr:
			if err != nil {
				return fmt.Errorf("http: %w", err)
			}
		case <-t.C:
			publish()
		}
	}
}

func checkCmd(args []string) (int, error) {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	wait := fs.Duration("wait", 15*time.Second, "how long to wait for Solar Assistant data")
	pretty := fs.Bool("pretty", true, "indent the JSON")
	c, err := loadCommon(fs, args)
	if err != nil {
		return 3, err
	}
	if c.log.Enabled(context.Background(), slog.LevelDebug) {
		c.log = c.log.With("mode", "check")
	} else {
		// check is for scripts: keep stderr quiet unless something is wrong.
		c.log = newLogger("warn")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	s := build(c, false)

	if err := s.sa.Start(ctx); err != nil {
		return 3, err
	}
	connectCtx, cancel := context.WithTimeout(ctx, *wait)
	err = s.bus.Connect(connectCtx)
	cancel()
	if err != nil {
		s.log.Warn("mqtt unavailable; power side will read as unknown", "err", err)
	}
	defer s.bus.Close()

	// Dish and forecast: one inline poll each (Start does the first status
	// poll inline; history is normally on the 60 s loop, so force it).
	if s.sl != nil {
		s.sl.PollStatus(ctx)
		s.sl.PollHistory(ctx)
		s.sl.PollDiagnostics(ctx)
	}
	if s.om != nil {
		if err := s.om.LoadCache(); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("forecast cache", "err", err)
		}
		if err := s.om.Fetch(ctx); err != nil {
			s.log.Warn("forecast fetch failed", "err", err)
		}
	}

	// Wait for Solar Assistant to say something (retained topics arrive
	// right after subscribe; live topics within a few seconds).
	deadline := time.Now().Add(*wait)
	for s.sa.MessageCount() == 0 && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(200 * time.Millisecond)
	}
	if s.sa.MessageCount() > 0 {
		// Give the burst a moment so totals and both inverters are in.
		time.Sleep(1500 * time.Millisecond)
	}

	st := s.app.Evaluate(ctx)
	enc := json.NewEncoder(os.Stdout)
	if *pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(st); err != nil {
		return 3, err
	}
	return engine.ExitCode(st.Verdict.CallOK), nil
}
