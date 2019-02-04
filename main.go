package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"github.com/tuenti/secrets-manager/backend"
	"github.com/tuenti/secrets-manager/controller"
	k8s "github.com/tuenti/secrets-manager/kubernetes"
	"github.com/tuenti/secrets-manager/secrets-manager"

	smclientset "github.com/tuenti/secrets-manager/pkg/client/clientset/versioned"
	sminformers "github.com/tuenti/secrets-manager/pkg/client/informers/externalversions"
	v1alpha1 "github.com/tuenti/secrets-manager/pkg/apis/secretsmanager/v1alpha1"

	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	apiextension "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// To be filled from build ldflags
var version string

func homeDir() string {
	return os.Getenv("HOME")
}

func newK8sConfig(inCluster bool, kubeconfig *string) (*rest.Config, error) {
	var config *rest.Config
	var err error

	if inCluster {
		config, err = rest.InClusterConfig()
	} else {
		// use the current context in kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	}

	return config, err
}

func newK8sClientSet(config *rest.Config) (*kubernetes.Clientset, error) {
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientSet, nil
}

func newSmClientSet(config *rest.Config) (smclientset.Interface, error) {
	clientSet, err := smclientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientSet, nil
}

func main() {
	var logger *log.Logger
	var wg sync.WaitGroup
	var kubeconfig *string

	backendCfg := backend.Config{}
	secretsManagerCfg := secretsmanager.Config{}
	selectedBackend := flag.String("backend", "vault", "Selected backend. Only vault supported")
	logLevel := flag.String("log.level", "warn", "Minimum log level")
	logFormat := flag.String("log.format", "text", "Log format, one of text or json")
	versionFlag := flag.Bool("version", false, "Display Secret Manager version")
	addr := flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")


	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	inCluster := flag.Bool("in-cluster", true, "Use in-cluster Kubernetes config")

	flag.StringVar(&secretsManagerCfg.ConfigMap, "config.config-map", "secrets-manager-config", "Name of the config Map with Secrets Manager settings (format: [<namespace>/]<name>) ")
	flag.DurationVar(&secretsManagerCfg.BackendScrapeInterval, "config.backend-timeout", 5*time.Second, "Backend connection timeout")
	flag.DurationVar(&secretsManagerCfg.BackendScrapeInterval, "config.backend-scrape-interval", 15*time.Second, "Scraping secrets from backend interval")
	flag.DurationVar(&secretsManagerCfg.ConfigMapRefreshInterval, "config.configmap-refresh-interval", 15*time.Second, "ConfigMap refresh interval")

	flag.StringVar(&backendCfg.VaultURL, "vault.url", "https://127.0.0.1:8200", "Vault address. VAULT_ADDR environment would take precedence.")
	flag.StringVar(&backendCfg.VaultToken, "vault.token", "", "Vault token. VAULT_TOKEN environment would take precedence.")
	flag.Int64Var(&backendCfg.VaultMaxTokenTTL, "vault.max-token-ttl", 300, "Max seconds to consider a token expired.")
	flag.DurationVar(&backendCfg.VaultTokenPollingPeriod, "vault.token-polling-period", 15*time.Second, "Polling interval to check token expiration time.")
	flag.IntVar(&backendCfg.VaultRenewTTLIncrement, "vault.renew-ttl-increment", 600, "TTL time for renewed token.")
	flag.StringVar(&backendCfg.VaultEngine, "vault.engine", "kv2", "Vault secret engine. Only KV version 1 and 2 supported")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("Secrets Manager %s\n", version)
		os.Exit(0)
	}

	logger = log.New()

	switch *logLevel {
	case "info":
		logger.SetLevel(log.InfoLevel)
	case "err":
		logger.SetLevel(log.ErrorLevel)
	case "debug":
		logger.SetLevel(log.DebugLevel)
	default:
		logger.SetLevel(log.WarnLevel)
	}

	switch *logFormat {
	case "json":
		logger.Formatter = &log.JSONFormatter{}
	default:
		logger.Formatter = &log.TextFormatter{}
	}

	logger.SetOutput(os.Stdout)

	if os.Getenv("VAULT_ADDR") != "" {
		backendCfg.VaultURL = os.Getenv("VAULT_ADDR")
	}

	if os.Getenv("VAULT_TOKEN") != "" {
		backendCfg.VaultToken = os.Getenv("VAULT_TOKEN")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backendClient, err := backend.NewBackendClient(ctx, *selectedBackend, logger, backendCfg)
	if err != nil {
		logger.Errorf("could not build backend client: %v", err)
		os.Exit(1)
	}

	k8sConfig, err := newK8sConfig(*inCluster, kubeconfig)
	if err != nil {
		logger.Errorf("could not build k8s client: %v", err)
		os.Exit(1)
	}

	clientSet, err := newK8sClientSet(k8sConfig)

	apiExtensionsClient, err := apiextension.NewForConfig(k8sConfig)
	if err != nil {
		logger.Fatalf("Failed to create client: %v", err)
	}
	err = v1alpha1.CreateCRD(apiExtensionsClient)
	if err != nil {
		logger.Fatalf("Failed to create crd: %v", err)
	}
 
	if err != nil {
		logger.Errorf("could not build k8s client: %v", err)
		os.Exit(1)
	}

	kubernetes := k8s.New(clientSet, logger)
	secretsManager, err := secretsmanager.New(ctx, secretsManagerCfg, kubernetes, *backendClient, logger)

	if err != nil {
		logger.Errorf("could not init Secret Manager: %v", err)
		os.Exit(1)
	}

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	wg.Add(1)

	secretDefinitionClient, err := newSmClientSet(k8sConfig)
	if err != nil {
		logger.Fatalf("Error building secretDefinition clientset: %s", err.Error())
	}

	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(clientSet, time.Second*30)
	secretDefinitionInformerFactory := sminformers.NewSharedInformerFactory(secretDefinitionClient, time.Second*30)

	controller := controller.NewController(
		clientSet,
		secretDefinitionClient,
		secretDefinitionInformerFactory.Secretsmanager().V1alpha1().SecretDefinitions(),
		secretsManager,
		logger,
	)

	stopCh := make(chan struct{}, 1)

	kubeInformerFactory.Start(stopCh)
	secretDefinitionInformerFactory.Start(stopCh)

	if err = controller.Run(2, stopCh); err != nil {
		logger.Fatalf("Error running controller: %s", err.Error())
	}

	srv := startHttpServer(*addr, logger)

	for {
		select {
		case <-sigc:
			shutdownHttpServer(srv, logger)
			close(stopCh)
			cancel()
			break
		}
		break
	}
	wg.Wait()
}

func shutdownHttpServer(srv *http.Server, logger *log.Logger) {
	logger.Infof("[main] Stopping HTTP server")

	if err := srv.Shutdown(nil); err != nil {
		logger.Errorf("ListenAndServe(): %s", err)
	} else {
		logger.Infof("[main] Stopped HTTP server")
	}
}

func startHttpServer(addr string, logger *log.Logger) *http.Server {
	srv := &http.Server{Addr: addr}

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		logger.Infof("Starting HTTP server listening on %v", addr)
		// returns ErrServerClosed on graceful close
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Errorf("[main] Unexpected error in HTTP server: %s", err)
		}
	}()

	return srv
}
