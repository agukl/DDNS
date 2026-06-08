package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ddns/internal/config"
	"ddns/internal/ipgetter"
	"ddns/internal/logging"
	"ddns/internal/poller"
	"ddns/internal/provider"
	"ddns/internal/provider/aliyun"
	"ddns/internal/provider/cloudflare"
	"ddns/internal/provider/dnspod"
	"ddns/internal/service"
	"ddns/internal/state"
	"ddns/internal/updater"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return 1
	}

	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "config.json", "config file path")
	serviceName := fs.String("name", service.DefaultName, "windows service name")
	displayName := fs.String("display-name", service.DefaultName, "windows service display name")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}

	switch command {
	case "install-service":
		if err := service.Install(*serviceName, *displayName, *configPath); err != nil {
			fmt.Fprintf(os.Stderr, "install service: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "service %q installed\n", *serviceName)
		return 0
	case "uninstall-service":
		if err := service.Uninstall(*serviceName); err != nil {
			fmt.Fprintf(os.Stderr, "uninstall service: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "service %q uninstalled\n", *serviceName)
		return 0
	case "start-service":
		if err := service.Start(*serviceName); err != nil {
			fmt.Fprintf(os.Stderr, "start service: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "service %q started\n", *serviceName)
		return 0
	case "stop-service":
		if err := service.Stop(*serviceName); err != nil {
			fmt.Fprintf(os.Stderr, "stop service: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "service %q stopped\n", *serviceName)
		return 0
	case "restart-service":
		if err := service.Restart(*serviceName); err != nil {
			fmt.Fprintf(os.Stderr, "restart service: %v\n", err)
			return 1
		}
		fmt.Fprintf(os.Stdout, "service %q restarted\n", *serviceName)
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		return 1
	}

	logger, closeLog, err := newLogger(cfg, command != "service")
	if err != nil {
		fmt.Fprintf(os.Stderr, "init logger: %v\n", err)
		return 1
	}
	defer closeLog()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	httpClient := &http.Client{Timeout: time.Duration(cfg.IPProbe.TimeoutSeconds) * time.Second}

	switch command {
	case "check-ip":
		getter := &ipgetter.Getter{
			Client:    httpClient,
			Endpoints: cfg.IPProbe.Endpoints,
			IPType:    cfg.IPProbe.Type,
			Timeout:   time.Duration(cfg.IPProbe.TimeoutSeconds) * time.Second,
		}
		ip, err := getter.GetOutboundIP(ctx)
		if err != nil {
			logger.Error("check ip failed", "error", err)
			return 2
		}
		fmt.Println(ip.String())
		return 0
	case "once":
		p, err := buildPoller(cfg, httpClient, logger)
		if err != nil {
			logger.Error("init sync failed", "error", err)
			return 1
		}
		if err := p.RunOnce(ctx); err != nil {
			logger.Error("sync failed", "error", err)
			return 3
		}
		return 0
	case "run":
		logger.Info("ddns started", "mode", command, "config", *configPath, "base_dir", cfg.BaseDir, "interval_seconds", cfg.IntervalSeconds)
		p, err := buildPoller(cfg, httpClient, logger)
		if err != nil {
			logger.Error("init sync failed", "error", err)
			return 1
		}
		if err := p.RunLoop(ctx); err != nil {
			logger.Error("run loop failed", "error", err)
			return 3
		}
		return 0
	case "service":
		logger.Info("ddns started", "mode", command, "config", *configPath, "base_dir", cfg.BaseDir, "interval_seconds", cfg.IntervalSeconds)
		p, err := buildPoller(cfg, httpClient, logger)
		if err != nil {
			logger.Error("init service sync failed", "error", err)
			return 1
		}
		if err := p.RunLoop(ctx); err != nil {
			logger.Error("service failed", "error", err)
			return 3
		}
		return 0
	default:
		usage()
		return 1
	}
}

func buildPoller(cfg config.Config, httpClient *http.Client, logger *slog.Logger) (*poller.Poller, error) {
	if err := cfg.ValidateRecords(); err != nil {
		return nil, err
	}
	registry, err := buildRegistry(cfg, httpClient)
	if err != nil {
		return nil, err
	}
	getter := &ipgetter.Getter{
		Client:    httpClient,
		Endpoints: cfg.IPProbe.Endpoints,
		IPType:    cfg.IPProbe.Type,
		Timeout:   time.Duration(cfg.IPProbe.TimeoutSeconds) * time.Second,
		Providers: registry,
	}
	store := state.New(cfg.ResolvePath(cfg.StateFile))
	aUpdater := &updater.AUpdater{
		Providers: registry,
		State:     store,
		Logger:    logger,
	}
	return &poller.Poller{
		Interval: time.Duration(cfg.IntervalSeconds) * time.Second,
		Sync: func(ctx context.Context) error {
			return syncOnce(ctx, cfg, getter, aUpdater, logger)
		},
		Logger: logger,
	}, nil
}

func syncOnce(ctx context.Context, cfg config.Config, getter *ipgetter.Getter, aUpdater *updater.AUpdater, logger *slog.Logger) error {
	outboundIP, err := getter.GetOutboundIP(ctx)
	if err != nil {
		return err
	}
	logger.Info("current outbound ip", "ip", outboundIP.String())

	var errs []error
	for _, target := range cfg.Records {
		current, err := getter.GetARecord(ctx, target)
		if err != nil {
			logger.Error("get current a record failed", "provider", target.Provider, "record", target.Name, "error", err)
			errs = append(errs, fmt.Errorf("%s: %w", target.Name, err))
			continue
		}

		_, err = aUpdater.EnsureA(ctx, target, current, outboundIP)
		if err != nil {
			logger.Error("ensure a record failed", "provider", target.Provider, "record", target.Name, "error", err)
			errs = append(errs, fmt.Errorf("%s: %w", target.Name, err))
		}
	}
	return errors.Join(errs...)
}

func buildRegistry(cfg config.Config, client *http.Client) (*provider.Registry, error) {
	registry := provider.NewRegistry()
	needed := make(map[string]bool)
	for _, record := range cfg.Records {
		needed[record.Provider] = true
	}

	for name := range needed {
		switch name {
		case "cloudflare":
			providerCfg, ok := cfg.Providers[name]
			if !ok {
				return nil, fmt.Errorf("providers.%s is required", name)
			}
			if providerCfg.APITokenEnv == "" {
				return nil, fmt.Errorf("providers.%s.api_token_env is required", name)
			}
			token := os.Getenv(providerCfg.APITokenEnv)
			if token == "" {
				return nil, fmt.Errorf("environment variable %s is empty", providerCfg.APITokenEnv)
			}
			registry.Register(name, cloudflare.New(token, client))
		case "dnspod", "tencent", "tencentcloud":
			providerCfg, ok := providerConfigByAlias(cfg, name, "dnspod", "tencent", "tencentcloud")
			if !ok {
				return nil, fmt.Errorf("providers.%s is required", name)
			}
			secretID, err := secretValue(providerCfg.SecretID, providerCfg.SecretIDEnv, "secret_id", "secret_id_env")
			if err != nil {
				return nil, fmt.Errorf("providers.%s: %w", name, err)
			}
			secretKey, err := secretValue(providerCfg.SecretKey, providerCfg.SecretKeyEnv, "secret_key", "secret_key_env")
			if err != nil {
				return nil, fmt.Errorf("providers.%s: %w", name, err)
			}
			if strings.HasPrefix(secretKey, "AKID") {
				return nil, fmt.Errorf("providers.%s.secret_key looks like a Tencent Cloud SecretId; use the SecretKey value instead", name)
			}
			registry.Register(name, dnspod.New(secretID, secretKey, client))
		case "aliyun", "alicloud":
			providerCfg, ok := providerConfigByAlias(cfg, name, "aliyun", "alicloud")
			if !ok {
				return nil, fmt.Errorf("providers.%s is required", name)
			}
			accessKeyID, err := secretValue(providerCfg.AccessKeyID, providerCfg.AccessKeyIDEnv, "access_key_id", "access_key_id_env")
			if err != nil {
				return nil, fmt.Errorf("providers.%s: %w", name, err)
			}
			accessKeySecret, err := secretValue(providerCfg.AccessKeySecret, providerCfg.AccessKeySecretEnv, "access_key_secret", "access_key_secret_env")
			if err != nil {
				return nil, fmt.Errorf("providers.%s: %w", name, err)
			}
			registry.Register(name, aliyun.New(accessKeyID, accessKeySecret, client))
		default:
			return nil, fmt.Errorf("provider %q is not implemented", name)
		}
	}
	return registry, nil
}

func secretValue(value, envName, valueField, envField string) (string, error) {
	if value != "" {
		return value, nil
	}
	if envName == "" {
		return "", fmt.Errorf("%s or %s is required", valueField, envField)
	}
	secret := os.Getenv(envName)
	if secret == "" {
		return "", fmt.Errorf("environment variable %s from %s is empty", envName, envField)
	}
	return secret, nil
}

func providerConfigByAlias(cfg config.Config, names ...string) (config.ProviderConfig, bool) {
	for _, name := range names {
		if providerCfg, ok := cfg.Providers[name]; ok {
			return providerCfg, true
		}
	}
	return config.ProviderConfig{}, false
}

func newLogger(cfg config.Config, includeConsole bool) (*slog.Logger, func(), error) {
	level := slog.LevelInfo
	switch strings.ToUpper(cfg.Logging.Level) {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO", "":
		level = slog.LevelInfo
	case "WARN":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		return nil, nil, fmt.Errorf("unsupported log level %q", cfg.Logging.Level)
	}

	var writer io.Writer
	if includeConsole {
		writer = os.Stdout
	} else {
		writer = io.Discard
	}
	var closeLog func() = func() {}
	if cfg.Logging.File != "" {
		logPath := cfg.ResolvePath(cfg.Logging.File)
		fileWriter, err := logging.NewRotatingLineWriter(logPath, cfg.Logging.MaxLines, cfg.Logging.MaxFiles)
		if err != nil {
			return nil, nil, err
		}
		if includeConsole {
			writer = io.MultiWriter(os.Stdout, fileWriter)
		} else {
			writer = fileWriter
		}
		closeLog = func() { _ = fileWriter.Close() }
	}

	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level})
	return slog.New(handler), closeLog, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  ddns.exe check-ip -config config.json")
	fmt.Fprintln(os.Stderr, "  ddns.exe once -config config.json")
	fmt.Fprintln(os.Stderr, "  ddns.exe run -config config.json")
	fmt.Fprintln(os.Stderr, "  ddns.exe install-service -config config.json [-name DDNS]")
	fmt.Fprintln(os.Stderr, "  ddns.exe uninstall-service [-name DDNS]")
	fmt.Fprintln(os.Stderr, "  ddns.exe start-service [-name DDNS]")
	fmt.Fprintln(os.Stderr, "  ddns.exe stop-service [-name DDNS]")
	fmt.Fprintln(os.Stderr, "  ddns.exe restart-service [-name DDNS]")
}
