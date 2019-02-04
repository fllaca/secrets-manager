package controller

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
	kubeinformers "k8s.io/client-go/informers"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"

	v1alpha1 "github.com/tuenti/secrets-manager/pkg/apis/secretsmanager/v1alpha1"
	"github.com/tuenti/secrets-manager/pkg/client/clientset/versioned/fake"
	informers "github.com/tuenti/secrets-manager/pkg/client/informers/externalversions"

	"github.com/tuenti/secrets-manager/mocks"
	gomock "github.com/golang/mock/gomock"

	log "github.com/sirupsen/logrus"
)

var (
	alwaysReady        = func() bool { return true }
	noResyncPeriodFunc = func() time.Duration { return 0 }
)

type fixture struct {
	t *testing.T
	mockCtrl *gomock.Controller

	client     *fake.Clientset
	kubeclient *k8sfake.Clientset
	// Objects to put in the store.
	secretDefinitionLister        []*v1alpha1.SecretDefinition
	secretLister []*corev1.Secret
	// Actions expected to happen on the client.
	kubeactions []core.Action
	actions     []core.Action
	// Objects from here preloaded into NewSimpleFake.
	kubeobjects []runtime.Object
	objects     []runtime.Object

	secretManagerMock *mocks.MockSecretManager
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{}
	f.t = t
	f.objects = []runtime.Object{}
	f.kubeobjects = []runtime.Object{}
	f.mockCtrl = gomock.NewController(t)
	f.secretManagerMock = mocks.NewMockSecretManager(f.mockCtrl)
	return f
}

func newSecretDefinition(name string, namespace string) *v1alpha1.SecretDefinition {
	return &v1alpha1.SecretDefinition{
		TypeMeta: metav1.TypeMeta{APIVersion: v1alpha1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metav1.NamespaceDefault,
		},
		Spec: v1alpha1.SecretDefinitionSpec{
			Name: fmt.Sprintf("%s-secret", name),
			Namespaces: []string{namespace},
		},
	}
}

func (f *fixture) newController() (*Controller, informers.SharedInformerFactory, kubeinformers.SharedInformerFactory) {
	f.client = fake.NewSimpleClientset(f.objects...)
	f.kubeclient = k8sfake.NewSimpleClientset(f.kubeobjects...)

	i := informers.NewSharedInformerFactory(f.client, noResyncPeriodFunc())
	k8sI := kubeinformers.NewSharedInformerFactory(f.kubeclient, noResyncPeriodFunc())

	c := NewController(
		f.kubeclient, 
		f.client,
		i.Secretsmanager().V1alpha1().SecretDefinitions(),
		f.secretManagerMock,
		log.New(),
	)

	c.secretDefinitionsSynced = alwaysReady
	c.recorder = &record.FakeRecorder{}

	for _, sd := range f.secretDefinitionLister {
		i.Secretsmanager().V1alpha1().SecretDefinitions().Informer().GetIndexer().Add(sd)
	}

	for _, s := range f.secretLister {
		k8sI.Core().V1().Secrets().Informer().GetIndexer().Add(s)
	}

	return c, i, k8sI
}

func (f *fixture) run(secretDefinitionName string) {
	f.runController(secretDefinitionName, true, false)
}

func (f *fixture) runExpectError(secretDefinitionName string) {
	f.runController(secretDefinitionName, true, true)
}

func (f *fixture) runController(secretDefinitionName string, startInformers bool, expectError bool) {
	c, i, k8sI := f.newController()
	if startInformers {
		stopCh := make(chan struct{})
		defer close(stopCh)
		i.Start(stopCh)
		k8sI.Start(stopCh)
	}

	err := c.syncHandler(secretDefinitionName)
	if !expectError && err != nil {
		f.t.Errorf("error syncing foo: %v", err)
	} else if expectError && err == nil {
		f.t.Error("expected error syncing foo, got nil")
	}

	actions := filterInformerActions(f.client.Actions())
	for i, action := range actions {
		if len(f.actions) < i+1 {
			f.t.Errorf("%d unexpected actions: %+v", len(actions)-len(f.actions), actions[i:])
			break
		}

		expectedAction := f.actions[i]
		checkAction(expectedAction, action, f.t)
	}

	if len(f.actions) > len(actions) {
		f.t.Errorf("%d additional expected actions:%+v", len(f.actions)-len(actions), f.actions[len(actions):])
	}

	k8sActions := filterInformerActions(f.kubeclient.Actions())
	for i, action := range k8sActions {
		if len(f.kubeactions) < i+1 {
			f.t.Errorf("%d unexpected actions: %+v", len(k8sActions)-len(f.kubeactions), k8sActions[i:])
			break
		}

		expectedAction := f.kubeactions[i]
		checkAction(expectedAction, action, f.t)
	}

	if len(f.kubeactions) > len(k8sActions) {
		f.t.Errorf("%d additional expected actions:%+v", len(f.kubeactions)-len(k8sActions), f.kubeactions[len(k8sActions):])
	}
}

// checkAction verifies that expected and actual actions are equal and both have
// same attached resources
func checkAction(expected, actual core.Action, t *testing.T) {
	if !(expected.Matches(actual.GetVerb(), actual.GetResource().Resource) && actual.GetSubresource() == expected.GetSubresource()) {
		t.Errorf("Expected\n\t%#v\ngot\n\t%#v", expected, actual)
		return
	}

	if reflect.TypeOf(actual) != reflect.TypeOf(expected) {
		t.Errorf("Action has wrong type. Expected: %t. Got: %t", expected, actual)
		return
	}

	switch a := actual.(type) {
	case core.CreateAction:
		e, _ := expected.(core.CreateAction)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintDiff(expObject, object))
		}
	case core.UpdateAction:
		e, _ := expected.(core.UpdateAction)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintDiff(expObject, object))
		}
	case core.PatchAction:
		e, _ := expected.(core.PatchAction)
		expPatch := e.GetPatch()
		patch := a.GetPatch()

		if !reflect.DeepEqual(expPatch, patch) {
			t.Errorf("Action %s %s has wrong patch\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintDiff(expPatch, patch))
		}
	}
}

// filterInformerActions filters list and watch actions for testing resources.
// Since list and watch don't change resource state we can filter it to lower
// nose level in our tests.
func filterInformerActions(actions []core.Action) []core.Action {
	ret := []core.Action{}
	for _, action := range actions {
		if len(action.GetNamespace()) == 0 &&
			(action.Matches("list", "foos") ||
				action.Matches("watch", "foos") ||
				action.Matches("list", "deployments") ||
				action.Matches("watch", "deployments")) {
			continue
		}
		ret = append(ret, action)
	}

	return ret
}

func (f *fixture) expectCreateSecretAction(d *corev1.Secret) {
	f.kubeactions = append(f.kubeactions, core.NewCreateAction(schema.GroupVersionResource{Resource: "secrets"}, d.Namespace, d))
}

func (f *fixture) expectUpdateSecretAction(d *corev1.Secret) {
	f.kubeactions = append(f.kubeactions, core.NewUpdateAction(schema.GroupVersionResource{Resource: "secrets"}, d.Namespace, d))
}

func (f *fixture) expectUpdateFooStatusAction(secretDefinition *v1alpha1.SecretDefinition) {
	action := core.NewUpdateAction(schema.GroupVersionResource{Resource: "secretDefinitions"}, secretDefinition.Namespace, secretDefinition)
	// TODO: Until #38113 is merged, we can't use Subresource
	//action.Subresource = "status"
	f.actions = append(f.actions, action)
}

func getKey(secretDefinition *v1alpha1.SecretDefinition, t *testing.T) string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(secretDefinition)
	if err != nil {
		t.Errorf("Unexpected error getting key for secretDefinition %v: %v", secretDefinition.Name, err)
		return ""
	}
	return key
}

func TestCreatesDeployment(t *testing.T) {
	f := newFixture(t)
	defer f.mockCtrl.Finish()
	secretDefinition := newSecretDefinition("test", "default")

	f.secretDefinitionLister = append(f.secretDefinitionLister, secretDefinition)
	f.objects = append(f.objects, secretDefinition)

	f.secretManagerMock.EXPECT().SyncState(secretDefinition.Spec).Times(1).Return(nil)

	f.run(getKey(secretDefinition, t))
}

// func TestDoNothing(t *testing.T) {
// 	f := newFixture(t)
// 	secretDefinition := newSecretDefinition("test", int32Ptr(1))
// 	d := newDeployment(secretDefinition)

// 	f.secretDefinitionLister = append(f.secretDefinitionLister, secretDefinition)
// 	f.objects = append(f.objects, secretDefinition)
// 	f.deploymentLister = append(f.deploymentLister, d)
// 	f.kubeobjects = append(f.kubeobjects, d)

// 	f.expectUpdateFooStatusAction(secretDefinition)
// 	f.run(getKey(secretDefinition, t))
// }

// func TestUpdateDeployment(t *testing.T) {
// 	f := newFixture(t)
// 	secretDefinition := newSecretDefinition("test", int32Ptr(1))
// 	d := newDeployment(secretDefinition)

// 	// Update replicas
// 	secretDefinition.Spec.Replicas = int32Ptr(2)
// 	expDeployment := newDeployment(secretDefinition)

// 	f.secretDefinitionLister = append(f.secretDefinitionLister, secretDefinition)
// 	f.objects = append(f.objects, secretDefinition)
// 	f.deploymentLister = append(f.deploymentLister, d)
// 	f.kubeobjects = append(f.kubeobjects, d)

// 	f.expectUpdateFooStatusAction(secretDefinition)
// 	f.expectUpdateDeploymentAction(expDeployment)
// 	f.run(getKey(secretDefinition, t))
// }

// func TestNotControlledByUs(t *testing.T) {
// 	f := newFixture(t)
// 	secretDefinition := newSecretDefinition("test", int32Ptr(1))
// 	d := newDeployment(secretDefinition)

// 	d.ObjectMeta.OwnerReferences = []metav1.OwnerReference{}

// 	f.secretDefinitionLister = append(f.secretDefinitionLister, secretDefinition)
// 	f.objects = append(f.objects, secretDefinition)
// 	f.deploymentLister = append(f.deploymentLister, d)
// 	f.kubeobjects = append(f.kubeobjects, d)

// 	f.runExpectError(getKey(secretDefinition, t))
// }

// func int32Ptr(i int32) *int32 { return &i }