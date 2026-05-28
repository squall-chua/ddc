// Package k8s is the read-only Kubernetes provider. It authenticates by reusing
// the user's existing kubeconfig (ddc never handles the raw token) and exposes
// only list/get/log operations. Mutating verbs are simply never called, and the
// Secret kind is refused outright so secret material is never returned.
package k8s

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/squallchua/ddc/internal/provider"
)

func init() { provider.Register("k8s", New) }

// New returns an unconnected Kubernetes provider.
func New() provider.Provider { return &Provider{} }

// Provider talks to a single cluster using read-only operations.
type Provider struct {
	clientset   kubernetes.Interface
	contextName string
	host        string
}

// blockedKinds are resource kinds ddc refuses to read because they carry secret
// material. The refusal happens before any API call.
var blockedKinds = map[string]bool{"secret": true}

// Result is a renderable list: a table (Headers/Rows) plus the typed Items for
// --json output.
type Result struct {
	Headers []string
	Rows    [][]string
	Items   any
}

func (p *Provider) Name() string { return "k8s" }

// Connect loads the kubeconfig (honoring env as a context override) and builds a
// clientset. The credential stays inside the kubeconfig/rest.Config; ddc never
// extracts or prints it.
func (p *Provider) Connect(ctx context.Context, env string) error {
	// Default loading rules honor the KUBECONFIG env var (colon-separated paths)
	// and fall back to ~/.kube/config.
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if env != "" {
		overrides.CurrentContext = env
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)

	restCfg, err := cc.ClientConfig()
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("build kubernetes client: %w", err)
	}
	p.clientset = cs
	p.host = restCfg.Host
	p.contextName = env
	if p.contextName == "" {
		if raw, err := cc.RawConfig(); err == nil {
			p.contextName = raw.CurrentContext
		}
	}
	return nil
}

// Status verifies connectivity with a read-only call and returns a safe identity.
func (p *Provider) Status(ctx context.Context) (provider.Identity, error) {
	if _, err := p.clientset.Discovery().ServerVersion(); err != nil {
		return provider.Identity{}, err
	}
	return provider.Identity{
		Provider: "k8s",
		Account:  p.contextName,
		Endpoint: p.host,
		Source:   string(credentialSourceKubeconfig),
	}, nil
}

// credentialSourceKubeconfig avoids importing the credential package just for a
// label; the value matches credential.SourceKubeconfig.
const credentialSourceKubeconfig = "kubeconfig"

// Get lists resources of the given kind. Secret kinds are refused before any API
// call. Unsupported kinds return an error rather than falling through to a
// generic passthrough.
func (p *Provider) Get(ctx context.Context, kind, namespace string, allNamespaces bool) (Result, error) {
	norm := normalizeKind(kind)
	if blockedKinds[norm] {
		return Result{}, fmt.Errorf("blocked: reading Secret objects is disabled — ddc never exposes secrets to agents")
	}
	ns := namespace
	if allNamespaces {
		ns = metav1.NamespaceAll
	}
	switch norm {
	case "pod":
		return p.getPods(ctx, ns)
	case "deployment":
		return p.getDeployments(ctx, ns)
	case "service":
		return p.getServices(ctx, ns)
	case "node":
		return p.getNodes(ctx)
	case "event":
		return p.getEvents(ctx, ns)
	default:
		return Result{}, fmt.Errorf("unsupported resource %q (supported: pods, deployments, services, nodes, events)", kind)
	}
}

func (p *Provider) getPods(ctx context.Context, ns string) (Result, error) {
	list, err := p.clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(list.Items))
	for _, pod := range list.Items {
		ready, total := 0, len(pod.Spec.Containers)
		var restarts int32
		status := string(pod.Status.Phase)
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
			restarts += cs.RestartCount
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				status = cs.State.Waiting.Reason
			} else if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
				status = cs.State.Terminated.Reason
			}
		}
		rows = append(rows, []string{
			pod.Namespace, pod.Name,
			fmt.Sprintf("%d/%d", ready, total),
			status, fmt.Sprintf("%d", restarts),
			humanizeAge(pod.CreationTimestamp.Time),
		})
	}
	return Result{Headers: []string{"NAMESPACE", "NAME", "READY", "STATUS", "RESTARTS", "AGE"}, Rows: rows, Items: list.Items}, nil
}

func (p *Provider) getDeployments(ctx context.Context, ns string) (Result, error) {
	list, err := p.clientset.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(list.Items))
	for _, d := range list.Items {
		desired := int32(0)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		rows = append(rows, []string{
			d.Namespace, d.Name,
			fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, desired),
			fmt.Sprintf("%d", d.Status.AvailableReplicas),
			humanizeAge(d.CreationTimestamp.Time),
		})
	}
	return Result{Headers: []string{"NAMESPACE", "NAME", "READY", "AVAILABLE", "AGE"}, Rows: rows, Items: list.Items}, nil
}

func (p *Provider) getServices(ctx context.Context, ns string) (Result, error) {
	list, err := p.clientset.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(list.Items))
	for _, s := range list.Items {
		ports := make([]string, 0, len(s.Spec.Ports))
		for _, port := range s.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", port.Port, port.Protocol))
		}
		rows = append(rows, []string{
			s.Namespace, s.Name, string(s.Spec.Type), s.Spec.ClusterIP,
			strings.Join(ports, ","), humanizeAge(s.CreationTimestamp.Time),
		})
	}
	return Result{Headers: []string{"NAMESPACE", "NAME", "TYPE", "CLUSTER-IP", "PORTS", "AGE"}, Rows: rows, Items: list.Items}, nil
}

func (p *Provider) getNodes(ctx context.Context) (Result, error) {
	list, err := p.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(list.Items))
	for _, n := range list.Items {
		status := "NotReady"
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				status = "Ready"
			}
		}
		rows = append(rows, []string{
			n.Name, status, n.Status.NodeInfo.KubeletVersion, humanizeAge(n.CreationTimestamp.Time),
		})
	}
	return Result{Headers: []string{"NAME", "STATUS", "VERSION", "AGE"}, Rows: rows, Items: list.Items}, nil
}

func (p *Provider) getEvents(ctx context.Context, ns string) (Result, error) {
	list, err := p.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return Result{}, err
	}
	items := list.Items
	sort.Slice(items, func(i, j int) bool {
		return items[i].LastTimestamp.Time.Before(items[j].LastTimestamp.Time)
	})
	rows := make([][]string, 0, len(items))
	for _, e := range items {
		obj := e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name
		rows = append(rows, []string{
			humanizeAge(e.LastTimestamp.Time), e.Type, e.Reason, obj, e.Message,
		})
	}
	return Result{Headers: []string{"LAST-SEEN", "TYPE", "REASON", "OBJECT", "MESSAGE"}, Rows: rows, Items: items}, nil
}

// DescribePod returns a detailed, crashloop-oriented view of a single pod plus
// the raw object for --json.
func (p *Provider) DescribePod(ctx context.Context, namespace, name string) (string, any, error) {
	pod, err := p.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Name:      %s\n", pod.Name)
	fmt.Fprintf(&b, "Namespace: %s\n", pod.Namespace)
	fmt.Fprintf(&b, "Node:      %s\n", pod.Spec.NodeName)
	fmt.Fprintf(&b, "Status:    %s\n", pod.Status.Phase)
	if pod.Status.Reason != "" {
		fmt.Fprintf(&b, "Reason:    %s\n", pod.Status.Reason)
	}
	fmt.Fprintf(&b, "Age:       %s\n\n", humanizeAge(pod.CreationTimestamp.Time))

	b.WriteString("Containers:\n")
	for _, cs := range pod.Status.ContainerStatuses {
		fmt.Fprintf(&b, "  %s: ready=%t restarts=%d\n", cs.Name, cs.Ready, cs.RestartCount)
		switch {
		case cs.State.Waiting != nil:
			fmt.Fprintf(&b, "    State: Waiting (%s) %s\n", cs.State.Waiting.Reason, cs.State.Waiting.Message)
		case cs.State.Terminated != nil:
			fmt.Fprintf(&b, "    State: Terminated (%s) exit=%d\n", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
		case cs.State.Running != nil:
			fmt.Fprintf(&b, "    State: Running since %s\n", humanizeAge(cs.State.Running.StartedAt.Time))
		}
		if cs.LastTerminationState.Terminated != nil {
			lt := cs.LastTerminationState.Terminated
			fmt.Fprintf(&b, "    Last terminated: %s exit=%d\n", lt.Reason, lt.ExitCode)
		}
	}

	events, err := p.clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "involvedObject.name=" + name,
	})
	if err == nil && len(events.Items) > 0 {
		b.WriteString("\nEvents:\n")
		for _, e := range events.Items {
			fmt.Fprintf(&b, "  %s  %s  %s: %s\n", humanizeAge(e.LastTimestamp.Time), e.Type, e.Reason, e.Message)
		}
	}
	return b.String(), pod, nil
}

// Logs returns container logs. The caller is responsible for redacting the
// returned text before display.
func (p *Provider) Logs(ctx context.Context, namespace, pod, container string, previous bool, tail int64) (string, error) {
	opts := &corev1.PodLogOptions{Container: container, Previous: previous}
	if tail > 0 {
		opts.TailLines = &tail
	}
	req := p.clientset.CoreV1().Pods(namespace).GetLogs(pod, opts)
	rc, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func normalizeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "po", "pod", "pods":
		return "pod"
	case "deploy", "deployment", "deployments":
		return "deployment"
	case "svc", "service", "services":
		return "service"
	case "no", "node", "nodes":
		return "node"
	case "ev", "event", "events":
		return "event"
	case "secret", "secrets":
		return "secret"
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func humanizeAge(t time.Time) string {
	if t.IsZero() {
		return "<unknown>"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
