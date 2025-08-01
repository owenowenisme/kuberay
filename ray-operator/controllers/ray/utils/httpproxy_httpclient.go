package utils

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

type RayHttpProxyClientInterface interface {
	InitClient()
	CheckProxyActorHealth(ctx context.Context) error
	SetHostIp(hostIp, podNamespace, podName string, port int)
}

func GetRayHttpProxyClientFunc(mgr ctrl.Manager, useKubernetesProxy bool) func() RayHttpProxyClientInterface {
	return func() RayHttpProxyClientInterface {
		return &RayHttpProxyClient{
			mgr:                mgr,
			useKubernetesProxy: useKubernetesProxy,
		}
	}
}

type RayHttpProxyClient struct {
	client             *http.Client
	mgr                ctrl.Manager
	httpProxyURL       string
	useKubernetesProxy bool
}

func (r *RayHttpProxyClient) InitClient() {
	r.client = &http.Client{
		Timeout: 2 * time.Second,
	}
}

func (r *RayHttpProxyClient) SetHostIp(hostIp, podNamespace, podName string, port int) {
	if r.useKubernetesProxy {
		r.client = r.mgr.GetHTTPClient()
		r.httpProxyURL = fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s:%d/proxy/", r.mgr.GetConfig().Host, podNamespace, podName, port)
	}

	r.httpProxyURL = fmt.Sprintf("http://%s:%d/", hostIp, port)
}

// CheckProxyActorHealth checks the health status of the Ray Serve proxy actor.
func (r *RayHttpProxyClient) CheckProxyActorHealth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.httpProxyURL+RayServeProxyHealthPath, nil)
	if err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("CheckProxyActorHealth fails. status code: %d, status: %s, error reading body: %w", resp.StatusCode, resp.Status, err)
		}
		err = fmt.Errorf("CheckProxyActorHealth fails. status code: %d, status: %s, body: %s", resp.StatusCode, resp.Status, string(body))
		return err
	}
	// For responses with status code 200, we don't need to allocate memory for the response body.
	// Instead, we discard the contents directly to avoid unnecessary memory allocations.
	_, _ = io.Copy(io.Discard, resp.Body)

	return nil
}
