package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/traefik/traefik/v3/integration/try"
	"github.com/traefik/traefik/v3/pkg/api"
)

var updateExpected = flag.Bool("update_expected", false, "Update expected files in testdata")

// K8sSuite tests suite.
type K8sSuite struct {
	BaseSuite
	kindPortForwardCmd    *exec.Cmd
	kindTraefikValuesPath string
}

func TestK8sSuite(t *testing.T) {
	suite.Run(t, new(K8sSuite))
}

func (s *K8sSuite) SetupSuite() {
	if s.useKindMode() {
		s.setupKindSuite()
		return
	}

	s.BaseSuite.SetupSuite()

	s.createComposeProject("k8s")
	s.composeUp()

	abs, err := filepath.Abs("./fixtures/k8s/config.skip/kubeconfig.yaml")
	require.NoError(s.T(), err)

	err = try.Do(60*time.Second, func() error {
		_, err := os.Stat(abs)
		return err
	})
	require.NoError(s.T(), err)

	data, err := os.ReadFile(abs)
	require.NoError(s.T(), err)

	content := strings.ReplaceAll(string(data), "https://server:6443", fmt.Sprintf("https://%s", net.JoinHostPort(s.getComposeServiceIP("server"), "6443")))

	err = os.WriteFile(abs, []byte(content), 0o644)
	require.NoError(s.T(), err)

	err = os.Setenv("KUBECONFIG", abs)
	require.NoError(s.T(), err)
}

func (s *K8sSuite) TearDownSuite() {
	if s.useKindMode() {
		s.cleanupKindSuite()
	}

	s.BaseSuite.TearDownSuite()

	generatedFiles := []string{
		"./fixtures/k8s/config.skip/kubeconfig.yaml",
		"./fixtures/k8s/config.skip/k3s.log",
		"./fixtures/k8s/coredns.yaml",
		"./fixtures/k8s/rolebindings.yaml",
		"./fixtures/k8s/traefik.yaml",
		"./fixtures/k8s/ccm.yaml",
	}

	for _, filename := range generatedFiles {
		if err := os.Remove(filename); err != nil {
			log.Warn().Err(err).Send()
		}
	}
}

func (s *K8sSuite) useKindMode() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("K8S_USE_KIND")))
	return value == "1" || value == "true" || value == "yes"
}

func (s *K8sSuite) setupKindSuite() {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		require.NoError(s.T(), err)
		defaultConfig := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(defaultConfig); err == nil {
			err = os.Setenv("KUBECONFIG", defaultConfig)
			require.NoError(s.T(), err)
		} else {
			require.NoErrorf(s.T(), err, "KUBECONFIG must be set or %s must exist", defaultConfig)
		}
	}

	for _, fixture := range k8sBaseFixturePaths() {
		s.applyKindFixture(fixture)
	}

	s.ensureKindTraefikValues(sessionKindTraefikValuesDefaultFile)
}

func (s *K8sSuite) cleanupKindSuite() {
	s.cleanupKindSessionFixtures()
	if s.kindPortForwardCmd != nil {
		s.stopKindPortForward(s.kindPortForwardCmd)
		s.kindPortForwardCmd = nil
	}
	if s.kindTraefikValuesPath != "" {
		s.helm("uninstall", "traefik", "-n", "traefik")
		s.kindTraefikValuesPath = ""
	}
	for _, fixture := range k8sBaseFixturePathsReverse() {
		s.kubectl("delete", "--ignore-not-found", "-f", fixture)
	}
}

func (s *K8sSuite) applyKindFixture(fixture string) {
	if strings.HasSuffix(fixture, "00-experimental-v1.4.0.yml") || strings.HasSuffix(fixture, "01-traefik-crd.yml") {
		s.kubectl("apply", "--server-side", "--force-conflicts", "--validate=false", "-f", fixture)
		return
	}
	s.kubectl("apply", "-f", fixture)
}

func k8sBaseFixturePaths() []string {
	return k8sFixturePaths(kindFixtureMode())
}

func k8sBaseFixturePathsReverse() []string {
	paths := k8sBaseFixturePaths()
	for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
		paths[i], paths[j] = paths[j], paths[i]
	}
	return paths
}

type fixtureMode int

const (
	fixturesFull fixtureMode = iota
	fixturesSessionOnly
)

func kindFixtureMode() fixtureMode {
	flagValue := flag.Lookup("testify.m")
	if flagValue != nil && strings.Contains(flagValue.Value.String(), "SessionPersistence") {
		return fixturesSessionOnly
	}
	return fixturesFull
}

func k8sFixturePaths(mode fixtureMode) []string {
	switch mode {
	case fixturesSessionOnly:
		return []string{
			filepath.Join("fixtures", "k8s", "00-experimental-v1.4.0.yml"),
			filepath.Join("fixtures", "k8s", "01-traefik-crd.yml"),
		}
	default:
		return []string{
			filepath.Join("fixtures", "k8s", "00-experimental-v1.4.0.yml"),
			filepath.Join("fixtures", "k8s", "01-traefik-crd.yml"),
			filepath.Join("fixtures", "k8s", "02-secrets.yml"),
			filepath.Join("fixtures", "k8s", "02-services.yml"),
			filepath.Join("fixtures", "k8s", "03-gateway.yml"),
			filepath.Join("fixtures", "k8s", "03-ingress-https.yml"),
			filepath.Join("fixtures", "k8s", "03-ingress.yml"),
			filepath.Join("fixtures", "k8s", "03-ingressroute.yml"),
			filepath.Join("fixtures", "k8s", "03-tlsoption.yml"),
			filepath.Join("fixtures", "k8s", "03-tlsstore.yml"),
			filepath.Join("fixtures", "k8s", "04-ingressroute.yml"),
			filepath.Join("fixtures", "k8s", "05-ingressroutetcp.yml"),
			filepath.Join("fixtures", "k8s", "05-ingressrouteudp.yml"),
			filepath.Join("fixtures", "k8s", "06-ingressroute-traefikservices.yml"),
			filepath.Join("fixtures", "k8s", "07-ingressroute-cross-namespace.yml"),
			filepath.Join("fixtures", "k8s", "07-ingressroute-serverstransport.yml"),
			filepath.Join("fixtures", "k8s", "08-ingressclass.yml"),
		}
	}
}

func (s *K8sSuite) TestIngressConfiguration() {
	s.traefikCmd(withConfigFile("fixtures/k8s_default.toml"))

	s.testConfiguration("testdata/rawdata-ingress.json", "8080")
}

func (s *K8sSuite) TestIngressLabelSelector() {
	s.traefikCmd(withConfigFile("fixtures/k8s_ingress_label_selector.toml"))

	s.testConfiguration("testdata/rawdata-ingress-label-selector.json", "8080")
}

func (s *K8sSuite) TestCRDConfiguration() {
	s.traefikCmd(withConfigFile("fixtures/k8s_crd.toml"))

	s.testConfiguration("testdata/rawdata-crd.json", "8000")
}

func (s *K8sSuite) TestCRDLabelSelector() {
	s.traefikCmd(withConfigFile("fixtures/k8s_crd_label_selector.toml"))

	s.testConfiguration("testdata/rawdata-crd-label-selector.json", "8000")
}

func (s *K8sSuite) TestGatewayConfiguration() {
	s.traefikCmd(withConfigFile("fixtures/k8s_gateway.toml"))

	s.testConfiguration("testdata/rawdata-gateway.json", "8080")
}

func (s *K8sSuite) TestIngressclass() {
	s.traefikCmd(withConfigFile("fixtures/k8s_ingressclass.toml"))

	s.testConfiguration("testdata/rawdata-ingressclass.json", "8080")
}

func (s *K8sSuite) TestDisableIngressclassLookup() {
	s.traefikCmd(withConfigFile("fixtures/k8s_ingressclass_disabled.toml"))

	s.testConfiguration("testdata/rawdata-ingressclass-disabled.json", "8080")
}

func (s *K8sSuite) testConfiguration(path, apiPort string) {
	err := try.GetRequest("http://127.0.0.1:"+apiPort+"/api/entrypoints", 20*time.Second, try.BodyContains(`"name":"web"`))
	require.NoError(s.T(), err)

	expectedJSON := filepath.FromSlash(path)

	if *updateExpected {
		fi, err := os.Create(expectedJSON)
		require.NoError(s.T(), err)
		err = fi.Close()
		require.NoError(s.T(), err)
	}

	var buf bytes.Buffer
	err = try.GetRequest("http://127.0.0.1:"+apiPort+"/api/rawdata", 1*time.Minute, try.StatusCodeIs(http.StatusOK), matchesConfig(expectedJSON, &buf))

	if !*updateExpected {
		require.NoError(s.T(), err)
		return
	}

	if err != nil {
		log.Info().Msgf("In file update mode, got expected error: %v", err)
	}

	var rtRepr api.RunTimeRepresentation
	err = json.Unmarshal(buf.Bytes(), &rtRepr)
	require.NoError(s.T(), err)

	newJSON, err := json.MarshalIndent(rtRepr, "", "\t")
	require.NoError(s.T(), err)

	err = os.WriteFile(expectedJSON, newJSON, 0o644)
	require.NoError(s.T(), err)

	s.T().Fatal("We do not want a passing test in file update mode")
}

func matchesConfig(wantConfig string, buf *bytes.Buffer) try.ResponseCondition {
	return func(res *http.Response) error {
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}

		if err := res.Body.Close(); err != nil {
			return err
		}

		var obtained api.RunTimeRepresentation
		err = json.Unmarshal(body, &obtained)
		if err != nil {
			return err
		}

		if buf != nil {
			buf.Reset()
			if _, err := io.Copy(buf, bytes.NewReader(body)); err != nil {
				return err
			}
		}

		got, err := json.MarshalIndent(obtained, "", "\t")
		if err != nil {
			return err
		}

		expected, err := os.ReadFile(wantConfig)
		if err != nil {
			return err
		}

		// The pods IPs are dynamic, so we cannot predict them,
		// which is why we have to ignore them in the comparison.
		rxURL := regexp.MustCompile(`"(url|address)":\s+(".*")`)
		sanitizedExpected := rxURL.ReplaceAll(bytes.TrimSpace(expected), []byte(`"$1": "XXXX"`))
		sanitizedGot := rxURL.ReplaceAll(got, []byte(`"$1": "XXXX"`))

		rxServerStatus := regexp.MustCompile(`"(http://)?.*?":\s+(".*")`)
		sanitizedExpected = rxServerStatus.ReplaceAll(sanitizedExpected, []byte(`"XXXX": $1`))
		sanitizedGot = rxServerStatus.ReplaceAll(sanitizedGot, []byte(`"XXXX": $1`))

		if bytes.Equal(sanitizedExpected, sanitizedGot) {
			return nil
		}

		diff := difflib.UnifiedDiff{
			FromFile: "Expected",
			A:        difflib.SplitLines(string(sanitizedExpected)),
			ToFile:   "Got",
			B:        difflib.SplitLines(string(sanitizedGot)),
			Context:  3,
		}

		text, err := difflib.GetUnifiedDiffString(diff)
		if err != nil {
			return err
		}
		return errors.New(text)
	}
}
