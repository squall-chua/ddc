package k8s

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func newTestProvider(objs ...runtime.Object) *Provider {
	return &Provider{
		clientset:   fake.NewSimpleClientset(objs...),
		contextName: "test",
		host:        "https://test.example",
	}
}

func TestGetSecretsBlocked(t *testing.T) {
	p := newTestProvider()
	for _, kind := range []string{"secret", "secrets", "Secret"} {
		_, err := p.Get(context.Background(), kind, "default", false)
		if err == nil {
			t.Fatalf("expected %q to be blocked", kind)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "blocked") {
			t.Fatalf("expected a 'blocked' error for %q, got: %v", kind, err)
		}
	}
	if actions := p.clientset.(*fake.Clientset).Actions(); len(actions) != 0 {
		t.Fatalf("blocked kind must not hit the API, got actions: %v", actions)
	}
}

func TestGetPodsSurfacesCrashLoop(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "web",
				Ready:        false,
				RestartCount: 5,
				State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			}},
		},
	}
	p := newTestProvider(pod)
	res, err := p.Get(context.Background(), "pods", "default", false)
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(res.Rows[0], " ")
	for _, want := range []string{"web", "CrashLoopBackOff", "5", "0/1"} {
		if !strings.Contains(flat, want) {
			t.Fatalf("pods row missing %q: %q", want, flat)
		}
	}
}

func TestGetUsesOnlyReadVerbs(t *testing.T) {
	p := newTestProvider()
	for _, kind := range []string{"pods", "deployments", "services", "nodes", "events"} {
		if _, err := p.Get(context.Background(), kind, "", true); err != nil {
			t.Fatalf("get %s: %v", kind, err)
		}
	}
	for _, a := range p.clientset.(*fake.Clientset).Actions() {
		switch a.GetVerb() {
		case "list", "get", "watch":
		default:
			t.Fatalf("non-read verb %q issued on %q", a.GetVerb(), a.GetResource().Resource)
		}
	}
}

func TestUnsupportedKindRejected(t *testing.T) {
	p := newTestProvider()
	if _, err := p.Get(context.Background(), "configmaps", "default", false); err == nil {
		t.Fatal("expected unsupported kind to be rejected (no generic passthrough)")
	}
}
