package secretsmanager

import (
	"context"
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tuenti/secrets-manager/backend"
	"github.com/tuenti/secrets-manager/errors"
	k8s "github.com/tuenti/secrets-manager/kubernetes"

	v1alpha1 "github.com/tuenti/secrets-manager/pkg/apis/secretsmanager/v1alpha1"
)

type SecretManager interface {
	SyncState(secret v1alpha1.SecretDefinitionSpec) error 
}

type secretManager struct {
	configMapName            string
	configMapNamespace       string
	kubernetes               k8s.Client
	backend                  backend.Client
	backendScrapeInterval    time.Duration
	configMapRefreshInterval time.Duration
}

// https://golang.org/pkg/time/#pkg-constants
const timestampFormat = "2006-01-02T15.04.05Z"
const configMapKeySecretDefinitions = "secretDefinitions"

var logger *log.Logger

func New(ctx context.Context, config Config, kubernetes k8s.Client, backend backend.Client, l *log.Logger) (SecretManager, error) {
	secretManager := new(secretManager)
	secretManager.kubernetes = kubernetes
	secretManager.backend = backend
	logger = l

	return secretManager, nil
}

// getDesiredState will get the secrets from the backend source of truth
func (s *secretManager) getDesiredState(secret v1alpha1.SecretDefinitionSpec) (map[string][]byte, error) {
	desiredState := make(map[string][]byte)
	var err error
	for k, v := range secret.Data {
		bSecret, err := s.backend.ReadSecret(v.Path, v.Key)
		if err != nil {
			logger.Errorf("unable to read secret '%s/%s' from backend: %v", v.Path, v.Key, err)
			return nil, err
		}

		decoder, err := backend.NewDecoder(v.Encoding)
		if err != nil {
			logger.Errorf("refusing to use encoding %s: %v", v.Encoding, err)
			return nil, err
		}
		desiredState[k], err = decoder.DecodeString(bSecret)
		if err != nil {
			logger.Errorf("unable to decode %s data for '%s/%s': %v", v.Encoding, v.Path, v.Key, err)
			return nil, err
		}
	}
	return desiredState, err
}

// getCurrentState will get the secrets from Kubernetes API
func (s *secretManager) getCurrentState(namespace string, name string) (map[string][]byte, error) {
	currentState, err := s.kubernetes.ReadSecret(namespace, name)
	if err != nil {
		logger.Debugf("failed to read '%s/%s' secret from kubernetes api: %v", namespace, name, err)
	}
	return currentState, err
}

func (s *secretManager) SyncState(secret v1alpha1.SecretDefinitionSpec) error {
	desiredState, err := s.getDesiredState(secret)
	if err != nil {
		logger.Errorf("unable to get desired state for secret '%s' : %v", secret.Name, err)
		for _, namespace := range secret.Namespaces {
			secretSyncErrorsCount.WithLabelValues(secret.Name, namespace).Inc()
		}
		return err
	}
	for _, namespace := range secret.Namespaces {
		currentState, err := s.getCurrentState(namespace, secret.Name)
		if err != nil && !errors.IsK8sSecretNotFound(err) {
			logger.Errorf("unable to get current state of secret '%s/%s' : %v", namespace, secret.Name, err)
			secretSyncErrorsCount.WithLabelValues(secret.Name, namespace).Inc()
			// If we fail to read from Kubernetes, we keep trying with another namespace
			continue
		}
		eq := reflect.DeepEqual(desiredState, currentState)
		if !eq {
			logger.Infof("secret '%s/%s' must be updated", namespace, secret.Name)
			if err := s.upsertSecret(secret.Type, namespace, secret.Name, desiredState); err != nil {
				log.Errorf("unable to upsert secret %s/%s: %v", namespace, secret.Name, err)
				secretSyncErrorsCount.WithLabelValues(secret.Name, namespace).Inc()
				continue
			}
			logger.Infof("secret '%s/%s' updated", namespace, secret.Name)
		}
	}
	return nil
}

func (s *secretManager) upsertSecret(secretType string, namespace string, name string, data map[string][]byte) error {
	lastUpdate := time.Now()
	secret := &k8s.Secret{
		Type: secretType,
		Name: name,
		Labels: map[string]string{
			"managedBy":  "secrets-manager",
			"lastUpdate": lastUpdate.Format(timestampFormat),
		},
		Namespace: namespace,
		Data:      data,
	}
	err := s.kubernetes.UpsertSecret(secret)
	if err != nil {
		log.Errorf("unable to upsert secret %s/%s: %v", namespace, name, err)
		return err
	}
	secretLastUpdated.WithLabelValues(name, namespace).Set(float64(lastUpdate.Unix()))
	return nil
}
