package home

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/AdguardTeam/AdGuardHome/internal/aghtls"
	"github.com/AdguardTeam/AdGuardHome/internal/cluster"
	"github.com/AdguardTeam/golibs/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestWebClusterManagementRequiresAuthentication(t *testing.T) {
	storeGlobals(t)

	workDir := t.TempDir()
	clusterConf := cluster.DefaultConfig()
	clusterConf.Enabled = true
	clusterConf.Name = "master"
	clusterConf.Listen = "127.0.0.1:0"
	clusterConf.APIBase = "http://127.0.0.1:8091"
	clusterConf.Token = "test-cluster-token"
	require.NoError(t, cluster.SaveConfig(workDir, clusterConf))

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.DefaultCost)
	require.NoError(t, err)
	ctx := testutil.ContextWithTimeout(t, testTimeout)
	auth, err := newAuth(ctx, &authConfig{
		baseLogger:     testLogger,
		rateLimiter:    emptyRateLimiter{},
		trustedProxies: testTrustedProxies,
		dbFilename:     filepath.Join(workDir, "sessions.db"),
		users: []webUser{{
			Name:         testUsername,
			PasswordHash: string(passwordHash),
		}},
		sessionTTL: testTTL * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { auth.close(testutil.ContextWithTimeout(t, testTimeout)) })

	tlsManager, err := aghtls.NewDefaultManager(ctx, &aghtls.DefaultManagerConfig{
		Logger: testLogger,
	})
	require.NoError(t, err)
	web := newTestWeb(t, &webConfig{
		clientBuildFS: fstest.MapFS{
			"build/static/index.html": &fstest.MapFile{Data: []byte("test")},
		},
		workDir:    workDir,
		auth:       auth,
		tlsManager: tlsManager,
	})
	handler := web.wrapMux(testLogger)

	r := httptest.NewRequest(http.MethodGet, "/control/cluster/ui/status", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	loginCookie := generateAuthCookie(t, handler, testUsername, testPassword)
	r = httptest.NewRequest(http.MethodGet, "/control/cluster/ui/status", nil)
	r.AddCookie(loginCookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"name":"master"`)
	assert.False(t, strings.Contains(w.Body.String(), clusterConf.Token), "response leaked cluster token")
}

func TestWebCloseStopsCluster(t *testing.T) {
	probe := httptest.NewServer(http.NotFoundHandler())
	addr := probe.Listener.Addr().String()
	probe.Close()

	workDir := t.TempDir()
	clusterConf := cluster.DefaultConfig()
	clusterConf.Enabled = true
	clusterConf.Listen = addr
	clusterConf.APIBase = "http://" + addr
	clusterConf.Token = "test-cluster-token"
	require.NoError(t, cluster.SaveConfig(workDir, clusterConf))
	web := newTestWeb(t, &webConfig{workDir: workDir})
	ctx := testutil.ContextWithTimeout(t, testTimeout)
	require.NoError(t, web.cluster.Start(ctx))
	t.Cleanup(func() {
		_ = web.cluster.Close(testutil.ContextWithTimeout(t, testTimeout))
	})

	req, err := http.NewRequest(http.MethodGet, clusterConf.APIBase+"/control/cluster/ping", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+clusterConf.Token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	web.close(ctx)
	_, err = http.DefaultClient.Do(req)
	require.Error(t, err, "cluster node API is still available after web shutdown")
}
