package integration

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/traefik/traefik/v3/integration/try"
)

const (
	sessionBaseURL                   = "http://127.0.0.1:8180"
	sessionAPIURL                    = "http://127.0.0.1:8080/api/entrypoints"
	sessionFixturesDir               = "/fixtures/k8s-session-persistence"
	sessionConfigFile                = "fixtures/k8s_session_persistence.toml"
	sessionConfigFileNoExperimental  = "fixtures/k8s_session_persistence_no_experimental.toml"
	sessionDefaultRequestTimeout     = 5 * time.Second
	sessionDefaultReadinessTimeout   = 30 * time.Second
	sessionDefaultDeploymentTimeout  = 180 * time.Second
	sessionDefaultTrafficRetryWindow = 20 * time.Second
	sessionKindTrafficRetryWindow    = 60 * time.Second
	sessionPortForwardPort           = "8180:80"

	sessionKindTraefikValuesDir                = "script/kind-session-persistence/values"
	sessionKindTraefikValuesDefaultFile        = "traefik-kind.yaml"
	sessionKindTraefikValuesNoExperimentalFile = "traefik-kind-no-experimental.yaml"
)

func (s *K8sSuite) waitForSessionTraefik() {
	err := try.GetRequest(sessionAPIURL, sessionDefaultReadinessTimeout, try.BodyContains(`"name":"web"`))
	require.NoError(s.T(), err)
}

func (s *K8sSuite) kubectl(args ...string) string {
	if s.useKindMode() {
		cmd := exec.CommandContext(s.T().Context(), "kubectl", args...)
		output, err := cmd.CombinedOutput()
		require.NoErrorf(s.T(), err, "kubectl %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
		return string(output)
	}

	ctx := s.T().Context()
	con, ok := s.containers["server"]
	require.True(s.T(), ok, "k3s server container not found")

	cmd := append([]string{"kubectl"}, args...)
	exitCode, reader, err := con.Exec(ctx, cmd)
	require.NoError(s.T(), err)

	content, err := io.ReadAll(reader)
	require.NoError(s.T(), err)

	require.Equalf(s.T(), 0, exitCode, "kubectl %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(content)))

	return string(content)
}

func (s *K8sSuite) kubectlOptional(args ...string) (string, error) {
	cmd := exec.CommandContext(s.T().Context(), "kubectl", args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (s *K8sSuite) helm(args ...string) string {
	cmd := exec.CommandContext(s.T().Context(), "helm", args...)
	output, err := cmd.CombinedOutput()
	require.NoErrorf(s.T(), err, "helm %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	return string(output)
}

func (s *K8sSuite) sessionFixturePath(name string) string {
	if s.useKindMode() {
		return filepath.Join(repoRoot(), "traefik", "script", "kind-session-persistence", "tests", name)
	}
	return path.Join(sessionFixturesDir, name)
}

func (s *K8sSuite) ensureKindTraefikValues(valuesFile string) {
	valuesPath := filepath.Join(repoRoot(), "traefik", sessionKindTraefikValuesDir, valuesFile)
	if s.kindTraefikValuesPath == valuesPath {
		return
	}

	s.ensureKindNamespace()

	chartPath := filepath.Join(repoRoot(), "traefik-helm-chart", "traefik")
	s.helm("upgrade", "--install", "traefik", chartPath, "--namespace", "traefik", "--create-namespace", "-f", valuesPath, "--skip-crds")
	s.kubectl("rollout", "status", "-n", "traefik", "deployment/traefik", fmt.Sprintf("--timeout=%ds", int(sessionDefaultDeploymentTimeout.Seconds())))

	s.kindTraefikValuesPath = valuesPath
}

func (s *K8sSuite) ensureKindPortForward() {
	if s.kindPortForwardCmd != nil {
		return
	}
	s.kindPortForwardCmd = s.startKindPortForward()
}

func (s *K8sSuite) startKindPortForward() *exec.Cmd {
	cmd := exec.CommandContext(s.T().Context(), "kubectl", "-n", "traefik", "port-forward", "svc/traefik", sessionPortForwardPort)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Start()
	require.NoError(s.T(), err)

	err = try.Do(30*time.Second, func() error {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:8180", time.Second)
		if err != nil {
			return err
		}
		_ = conn.Close()
		return nil
	})
	if err != nil {
		s.stopKindPortForward(cmd)
		require.NoErrorf(s.T(), err, "port-forward failed to become ready: %s", strings.TrimSpace(out.String()))
	}

	return cmd
}

func (s *K8sSuite) stopKindPortForward(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}

	_ = try.Do(5*time.Second, func() error {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:8180", time.Second)
		if err != nil {
			return nil
		}
		_ = conn.Close()
		return errors.New("port-forward still listening")
	})
}

func (s *K8sSuite) ensureKindNamespace() {
	output, err := s.kubectlOptional("get", "namespace", "traefik", "-o", "jsonpath={.status.phase}")
	if err != nil {
		s.kubectl("create", "namespace", "traefik")
		return
	}

	if strings.TrimSpace(output) == "Terminating" {
		s.kubectl("wait", "--for=delete", "namespace/traefik", "--timeout=120s")
		s.kubectl("create", "namespace", "traefik")
	}
}

func repoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return ".."
	}

	switch filepath.Base(wd) {
	case "integration":
		return filepath.Dir(filepath.Dir(wd))
	case "traefik":
		return filepath.Dir(wd)
	default:
		return wd
	}
}

func (s *K8sSuite) applySessionFixtures(name string) {
	dir := s.sessionFixturePath(name)
	manifestFiles := sessionManifestFiles(dir)
	for _, file := range manifestFiles {
		s.kubectl("apply", "-f", file)
	}

	for _, file := range manifestFiles {
		resources := strings.Fields(strings.TrimSpace(s.kubectl("get", "-f", file, "-o", "name")))
		for _, resource := range resources {
			if !strings.HasPrefix(resource, "deployment/") {
				continue
			}
			s.kubectl("wait", "--for=condition=Available", resource, fmt.Sprintf("--timeout=%ds", int(sessionDefaultDeploymentTimeout.Seconds())))
		}
	}
}

func (s *K8sSuite) deleteSessionFixtures(name string) {
	dir := s.sessionFixturePath(name)
	for _, file := range sessionManifestFiles(dir) {
		s.kubectl("delete", "--ignore-not-found", "-f", file)
	}
}

func (s *K8sSuite) cleanupKindSessionFixtures() {
	if !s.useKindMode() {
		return
	}

	baseDir := filepath.Join("fixtures", "k8s-session-persistence")
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		s.deleteSessionFixtures(entry.Name())
	}
}

func (s *K8sSuite) request(host, path string, headers map[string]string) (*http.Response, string, error) {
	client := &http.Client{Timeout: sessionDefaultRequestTimeout}
	req, err := http.NewRequest(http.MethodGet, sessionBaseURL+path, nil)
	if err != nil {
		return nil, "", err
	}
	if host != "" {
		req.Host = host
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, "", err
	}

	if resp.StatusCode != http.StatusOK {
		return resp, string(body), fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	return resp, string(body), nil
}

func sessionManifestFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	manifests := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "traefik-values.yaml" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		manifests = append(manifests, filepath.Join(dir, name))
	}
	return manifests
}

func (s *K8sSuite) retryRequest(host, path string, headers map[string]string) (*http.Response, string) {
	var resp *http.Response
	var body string

	err := try.Do(s.sessionTrafficRetryWindow(), func() error {
		res, resBody, err := s.request(host, path, headers)
		if err != nil {
			return err
		}
		resp = res
		body = resBody
		return nil
	})
	require.NoError(s.T(), err)

	return resp, body
}

func (s *K8sSuite) sessionTrafficRetryWindow() time.Duration {
	if s.useKindMode() {
		return sessionKindTrafficRetryWindow
	}
	return sessionDefaultTrafficRetryWindow
}

func hostnameFromBody(body string) (string, error) {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "Hostname:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Hostname:")), nil
		}
	}

	return "", errors.New("hostname not found")
}

func cookieValueFromHeader(setCookie string) string {
	parts := strings.SplitN(setCookie, ";", 2)
	return strings.TrimSpace(parts[0])
}

func findCookieValue(setCookies []string, name string) (string, error) {
	prefix := name + "="
	for _, cookie := range setCookies {
		cookie = strings.TrimSpace(cookie)
		if strings.HasPrefix(cookie, prefix) {
			return cookieValueFromHeader(cookie), nil
		}
	}

	return "", fmt.Errorf("cookie %s not found", name)
}

func (s *K8sSuite) runSessionTest(name, configFile string, check func() error) {
	s.deleteSessionFixtures(name)

	if !s.useKindMode() {
		s.traefikCmd(withConfigFile(configFile))
		s.waitForSessionTraefik()
	}

	if s.useKindMode() {
		s.ensureKindPortForward()
	}

	s.applySessionFixtures(name)

	s.T().Cleanup(func() {
		s.deleteSessionFixtures(name)
		if s.useKindMode() && s.kindPortForwardCmd != nil {
			s.stopKindPortForward(s.kindPortForwardCmd)
			s.kindPortForwardCmd = nil
		}
	})

	require.NoError(s.T(), check())
}

func (s *K8sSuite) TestK8sSessionPersistenceBasicRoutingGateway() {
	s.runSessionTest("basic-routing-gateway", sessionConfigFile, func() error {
		_, body := s.retryRequest("whoami-gw-basic.localhost", "/", nil)
		_, err := hostnameFromBody(body)
		return err
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceBasicRoutingIngress() {
	s.runSessionTest("basic-routing-ingress", sessionConfigFile, func() error {
		_, body := s.retryRequest("whoami-ingress-basic.localhost", "/", nil)
		_, err := hostnameFromBody(body)
		return err
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceBasicRoutingIngressRoute() {
	s.runSessionTest("basic-routing-ingressroute", sessionConfigFile, func() error {
		_, body := s.retryRequest("whoami-ir-basic.localhost", "/", nil)
		_, err := hostnameFromBody(body)
		return err
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewaySticky() {
	s.runSessionTest("gateway-sticky", sessionConfigFile, func() error {
		resp, body := s.retryRequest("whoami.localhost", "/", nil)
		hostOne, err := hostnameFromBody(body)
		if err != nil {
			return err
		}

		cookie, err := findCookieValue(resp.Header.Values("Set-Cookie"), "traefik-sticky")
		if err != nil {
			return err
		}

		_, body = s.retryRequest("whoami.localhost", "/", map[string]string{"Cookie": cookie})
		hostTwo, err := hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("cookie stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		resp, body = s.retryRequest("whoami-header.localhost", "/", nil)
		hostOne, err = hostnameFromBody(body)
		if err != nil {
			return err
		}

		stickyHeader := resp.Header.Get("X-Session-ID")
		if stickyHeader == "" {
			stickyHeader = resp.Header.Get("X-Session-Id")
		}
		if stickyHeader == "" {
			return errors.New("missing X-Session-ID header")
		}

		_, body = s.retryRequest("whoami-header.localhost", "/", map[string]string{"X-Session-ID": stickyHeader})
		hostTwo, err = hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("header stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayStickyCookieAttrs() {
	s.runSessionTest("gateway-sticky-cookie-attrs", sessionConfigFile, func() error {
		resp, _ := s.retryRequest("whoami-cookie-attrs.localhost", "/", nil)
		setCookie := strings.Join(resp.Header.Values("Set-Cookie"), " ")
		if !strings.Contains(setCookie, "gw-cookie-attrs=") {
			return errors.New("expected gw-cookie-attrs cookie")
		}
		if !strings.Contains(setCookie, "Max-Age=") && !strings.Contains(setCookie, "Expires=") {
			return errors.New("expected Max-Age or Expires attribute on gw-cookie-attrs")
		}
		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayStickyFailover() {
	s.runSessionTest("gateway-sticky-failover", sessionConfigFile, func() error {
		resp, body := s.retryRequest("whoami-failover.localhost", "/", nil)
		hostOne, err := hostnameFromBody(body)
		if err != nil {
			return err
		}

		cookie, err := findCookieValue(resp.Header.Values("Set-Cookie"), "sticky-failover")
		if err != nil {
			return err
		}

		s.kubectl("-n", "default", "delete", "pod", "-l", "app=whoami-failover")

		_, body = s.retryRequest("whoami-failover.localhost", "/", map[string]string{"Cookie": cookie})
		hostTwo, err := hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne == hostTwo {
			return fmt.Errorf("expected failover to a new pod, got %s", hostTwo)
		}

		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayStickyMultiClient() {
	s.runSessionTest("gateway-sticky-multiclient", sessionConfigFile, func() error {
		resp, body := s.retryRequest("whoami-mc.localhost", "/", nil)
		hostOne, err := hostnameFromBody(body)
		if err != nil {
			return err
		}

		cookieOne, err := findCookieValue(resp.Header.Values("Set-Cookie"), "sticky-mc")
		if err != nil {
			return err
		}

		resp, body = s.retryRequest("whoami-mc.localhost", "/", nil)
		hostTwo, err := hostnameFromBody(body)
		if err != nil {
			return err
		}

		cookieTwo, err := findCookieValue(resp.Header.Values("Set-Cookie"), "sticky-mc")
		if err != nil {
			return err
		}

		_, body = s.retryRequest("whoami-mc.localhost", "/", map[string]string{"Cookie": cookieOne})
		hostOneFollow, err := hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostOneFollow {
			return fmt.Errorf("client A lost stickiness: %s != %s", hostOne, hostOneFollow)
		}

		_, body = s.retryRequest("whoami-mc.localhost", "/", map[string]string{"Cookie": cookieTwo})
		hostTwoFollow, err := hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostTwo != hostTwoFollow {
			return fmt.Errorf("client B lost stickiness: %s != %s", hostTwo, hostTwoFollow)
		}

		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayStickyMissing() {
	s.runSessionTest("gateway-sticky-missing", sessionConfigFile, func() error {
		unique := map[string]struct{}{}
		for i := 0; i < 10; i++ {
			_, body := s.retryRequest("whoami-missing.localhost", "/", nil)
			host, err := hostnameFromBody(body)
			if err != nil {
				return err
			}
			unique[host] = struct{}{}
		}
		if len(unique) < 2 {
			return fmt.Errorf("expected multiple backends without stickiness, got %d", len(unique))
		}
		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceIngressRouteSticky() {
	s.runSessionTest("ingressroute-sticky", sessionConfigFile, func() error {
		resp, body := s.retryRequest("whoami-ir-cookie.localhost", "/", nil)
		hostOne, err := hostnameFromBody(body)
		if err != nil {
			return err
		}

		cookie, err := findCookieValue(resp.Header.Values("Set-Cookie"), "ir-cookie")
		if err != nil {
			return err
		}

		_, body = s.retryRequest("whoami-ir-cookie.localhost", "/", map[string]string{"Cookie": cookie})
		hostTwo, err := hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("ingressroute cookie stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		resp, body = s.retryRequest("whoami-ir-header.localhost", "/", nil)
		hostOne, err = hostnameFromBody(body)
		if err != nil {
			return err
		}

		stickyHeader := resp.Header.Get("X-IR-Sticky")
		if stickyHeader == "" {
			return errors.New("missing X-IR-Sticky header")
		}

		_, body = s.retryRequest("whoami-ir-header.localhost", "/", map[string]string{"X-IR-Sticky": stickyHeader})
		hostTwo, err = hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("ingressroute header stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayTraefikServiceRouteSticky() {
	s.runSessionTest("gateway-traefikservice-route-sticky", sessionConfigFile, func() error {
		resp, body := s.retryRequest("whoami-tsr-header.localhost", "/", nil)
		hostOne, err := hostnameFromBody(body)
		if err != nil {
			return err
		}

		routeHeader := resp.Header.Get("X-Route-Sticky")
		if routeHeader == "" {
			return errors.New("missing X-Route-Sticky header")
		}

		_, body = s.retryRequest("whoami-tsr-header.localhost", "/", map[string]string{"X-Route-Sticky": routeHeader})
		hostTwo, err := hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("route header stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		resp, body = s.retryRequest("whoami-tsr-cookie.localhost", "/", nil)
		hostOne, err = hostnameFromBody(body)
		if err != nil {
			return err
		}

		cookie, err := findCookieValue(resp.Header.Values("Set-Cookie"), "route-cookie")
		if err != nil {
			return err
		}

		_, body = s.retryRequest("whoami-tsr-cookie.localhost", "/", map[string]string{"Cookie": cookie})
		hostTwo, err = hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("route cookie stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayTraefikServiceRouteWRR() {
	s.runSessionTest("gateway-traefikservice-route-wrr", sessionConfigFile, func() error {
		resp, body := s.retryRequest("whoami-tsr-wrr-header.localhost", "/", nil)
		hostOne, err := hostnameFromBody(body)
		if err != nil {
			return err
		}

		routeHeader := resp.Header.Get("X-Route-Sticky")
		if routeHeader == "" {
			return errors.New("missing X-Route-Sticky header")
		}

		_, body = s.retryRequest("whoami-tsr-wrr-header.localhost", "/", map[string]string{"X-Route-Sticky": routeHeader})
		hostTwo, err := hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("route header stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		resp, body = s.retryRequest("whoami-tsr-wrr-cookie.localhost", "/", nil)
		hostOne, err = hostnameFromBody(body)
		if err != nil {
			return err
		}

		cookie, err := findCookieValue(resp.Header.Values("Set-Cookie"), "route-cookie")
		if err != nil {
			return err
		}

		_, body = s.retryRequest("whoami-tsr-wrr-cookie.localhost", "/", map[string]string{"Cookie": cookie})
		hostTwo, err = hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("route cookie stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayTraefikServiceMultiLevel() {
	s.runSessionTest("gateway-traefikservice-multilevel", sessionConfigFile, func() error {
		resp, body := s.retryRequest("whoami-ts-cookie.localhost", "/", nil)
		hostOne, err := hostnameFromBody(body)
		if err != nil {
			return err
		}

		cookieWRR, err := findCookieValue(resp.Header.Values("Set-Cookie"), "ts-wrr-cookie")
		if err != nil {
			return err
		}

		cookieSvc, err := findCookieValue(resp.Header.Values("Set-Cookie"), "ts-svc-cookie")
		if err != nil {
			return err
		}

		cookieHeader := fmt.Sprintf("%s; %s", cookieWRR, cookieSvc)
		_, body = s.retryRequest("whoami-ts-cookie.localhost", "/", map[string]string{"Cookie": cookieHeader})
		hostTwo, err := hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("multilevel cookie stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		resp, body = s.retryRequest("whoami-ts-header.localhost", "/", nil)
		hostOne, err = hostnameFromBody(body)
		if err != nil {
			return err
		}

		headWRR := resp.Header.Get("X-TS-WRR")
		headSvc := resp.Header.Get("X-TS-SVC")
		if headWRR == "" || headSvc == "" {
			return errors.New("missing multilevel header stickiness values")
		}

		_, body = s.retryRequest("whoami-ts-header.localhost", "/", map[string]string{"X-TS-WRR": headWRR, "X-TS-SVC": headSvc})
		hostTwo, err = hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("multilevel header stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayTraefikServiceCookieAttrs() {
	s.runSessionTest("gateway-traefikservice-cookie-attrs", sessionConfigFile, func() error {
		resp, _ := s.retryRequest("whoami-ts-attrs.localhost", "/sticky", nil)
		setCookie := strings.Join(resp.Header.Values("Set-Cookie"), " ")
		required := []string{
			"ts-cookie-attrs=",
			"Path=/sticky",
			"Domain=whoami-ts-attrs.localhost",
			"Max-Age=600",
			"HttpOnly",
			"Secure",
			"SameSite=Strict",
		}
		for _, want := range required {
			if !strings.Contains(setCookie, want) {
				return fmt.Errorf("expected Set-Cookie to contain %q", want)
			}
		}
		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayXBackendTrafficPolicyHeader() {
	s.runSessionTest("gateway-xbackendpolicy-header", sessionConfigFile, func() error {
		resp, body := s.retryRequest("whoami-xbtp.localhost", "/", nil)
		hostOne, err := hostnameFromBody(body)
		if err != nil {
			return err
		}

		policyHeader := resp.Header.Get("X-Policy-Session")
		if policyHeader == "" {
			return errors.New("missing X-Policy-Session header")
		}

		_, body = s.retryRequest("whoami-xbtp.localhost", "/", map[string]string{"X-Policy-Session": policyHeader})
		hostTwo, err := hostnameFromBody(body)
		if err != nil {
			return err
		}
		if hostOne != hostTwo {
			return fmt.Errorf("xbackendtrafficpolicy stickiness mismatch: %s != %s", hostOne, hostTwo)
		}

		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayXBackendTrafficPolicyPrecedence() {
	s.runSessionTest("gateway-xbackendpolicy-precedence", sessionConfigFile, func() error {
		resp, _ := s.retryRequest("whoami-xbtp-precedence.localhost", "/", nil)
		if resp.Header.Get("X-Route-Session") == "" {
			return errors.New("missing X-Route-Session header")
		}
		if resp.Header.Get("X-Policy-Session") != "" {
			return errors.New("expected X-Policy-Session to be ignored when HTTPRoute defines session persistence")
		}
		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayXBackendTrafficPolicyTraefikServicePrecedence() {
	s.runSessionTest("gateway-xbackendpolicy-traefikservice", sessionConfigFile, func() error {
		resp, _ := s.retryRequest("whoami-xbtp-ts.localhost", "/", nil)
		if resp.Header.Get("X-Route-Session") == "" {
			return errors.New("missing X-Route-Session header")
		}
		if resp.Header.Get("X-TS-Sticky") != "" {
			return errors.New("expected TraefikService sticky header to be overridden by HTTPRoute")
		}
		if resp.Header.Get("X-Policy-Session") != "" {
			return errors.New("expected X-Policy-Session to be ignored when HTTPRoute defines session persistence")
		}
		return nil
	})
}

func (s *K8sSuite) TestK8sSessionPersistenceGatewayXBackendTrafficPolicyDisabled() {
	if s.useKindMode() {
		s.ensureKindTraefikValues(sessionKindTraefikValuesNoExperimentalFile)
		defer s.ensureKindTraefikValues(sessionKindTraefikValuesDefaultFile)
	}
	s.runSessionTest("gateway-xbackendpolicy-disabled", sessionConfigFileNoExperimental, func() error {
		resp, _ := s.retryRequest("whoami-xbtp-disabled.localhost", "/", nil)
		if resp.Header.Get("X-Policy-Session") != "" {
			return errors.New("expected X-Policy-Session to be absent when experimental channel is disabled")
		}

		unique := map[string]struct{}{}
		for i := 0; i < 10; i++ {
			_, body := s.retryRequest("whoami-xbtp-disabled.localhost", "/", nil)
			host, err := hostnameFromBody(body)
			if err != nil {
				return err
			}
			unique[host] = struct{}{}
		}
		if len(unique) < 2 {
			return fmt.Errorf("expected multiple backends when policy ignored, got %d", len(unique))
		}

		return nil
	})
}
