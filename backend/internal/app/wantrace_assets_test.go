package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func withWantraceTestServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	previous := wantraceAssetAPIBase
	wantraceAssetAPIBase = server.URL
	t.Cleanup(func() {
		wantraceAssetAPIBase = previous
		server.Close()
	})
}

func TestWantraceListChannelsUsesBearerAndDecodesEnvelope(t *testing.T) {
	withWantraceTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/seedance/assets/channels" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(`{"success":true,"message":"","data":{"channels":[{"id":1,"name":"seedance海外","group":"default"}]}}`))
	})
	channels, err := wantraceListChannels(context.Background(), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 1 || channels[0].ID != 1 || channels[0].Name != "seedance海外" {
		t.Fatalf("channels = %#v", channels)
	}
}

func TestWantraceAssetRequestSurfacesEnvelopeMessage(t *testing.T) {
	withWantraceTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"invalid api key"}`))
	})
	_, err := wantraceListChannels(context.Background(), "bad-key")
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("error = %v, want invalid api key", err)
	}
}

func TestWantraceAssetRequestRejectsEmptyKeyBeforeNetwork(t *testing.T) {
	called := false
	withWantraceTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	if _, err := wantraceListChannels(context.Background(), "  "); err == nil {
		t.Fatal("wantraceListChannels() error = nil, want missing key error")
	}
	if called {
		t.Fatal("empty API key should not reach the network")
	}
}

func TestWantraceCreateAssetSendsRealContract(t *testing.T) {
	withWantraceTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/seedance/assets/groups/group-remote/assets" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["asset_type"] != "Image" || body["name"] != "真人图" || body["url"] != "https://cdn.example/a.png" {
			t.Fatalf("body = %#v", body)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"asset-1","group_id":"group-remote","asset_type":"Image","status":"Processing"}}`))
	})
	asset, err := wantraceCreateAsset(context.Background(), "test-key", "group-remote", "真人图", "Image", "https://cdn.example/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if asset.ID != "asset-1" || asset.Status != "Processing" {
		t.Fatalf("asset = %#v", asset)
	}
}

func TestWantraceCheckVerificationDecodesGroup(t *testing.T) {
	withWantraceTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/seedance/assets/verifications/verify-1/check" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"verify-1","status":"verified","group":{"id":"group-1","kind":"real","upstream_id":"up-1","state":"ready"}}}`))
	})
	verification, err := wantraceCheckVerification(context.Background(), "test-key", "verify-1")
	if err != nil {
		t.Fatal(err)
	}
	if verification.Status != "verified" || verification.Group == nil || verification.Group.ID != "group-1" || verification.Group.Kind != "real" {
		t.Fatalf("verification = %#v", verification)
	}
}

func TestIsWantraceAssetChannelBaseURL(t *testing.T) {
	for _, raw := range []string{"https://newapi.wantrace.com/seedance", "https://newapi.wantrace.com"} {
		if !isWantraceAssetChannelBaseURL(raw) {
			t.Fatalf("isWantraceAssetChannelBaseURL(%q) = false", raw)
		}
	}
	for _, raw := range []string{"https://api.wantrace.com", "https://open.feeling.ltd", "https://ark.cn-beijing.volces.com/api/v3", ""} {
		if isWantraceAssetChannelBaseURL(raw) {
			t.Fatalf("isWantraceAssetChannelBaseURL(%q) = true", raw)
		}
	}
}

func TestWantraceAssetSettingDefaultsAndValidation(t *testing.T) {
	value := normalizeWantraceAssetSetting(wantraceAssetSettingValue{})
	if value.RealGroupLimit != defaultWantraceRealGroupLimit || value.RealAuthorizationDays != defaultWantraceAuthorization {
		t.Fatalf("normalized defaults = %#v", value)
	}
	if value.credentialsReady() {
		t.Fatal("empty setting should not be ready")
	}
	value.APIKey = " key "
	value.ChannelID = 1
	if !value.credentialsReady() {
		t.Fatal("setting with key and channel should be ready")
	}
}

func TestValidWantraceH5URL(t *testing.T) {
	if !validWantraceH5URL("https://h5.example.com/verify?token=1") {
		t.Fatal("valid https H5 URL rejected")
	}
	for _, raw := range []string{"http://h5.example.com", "https://user:pass@h5.example.com", "javascript:alert(1)", ""} {
		if validWantraceH5URL(raw) {
			t.Fatalf("validWantraceH5URL(%q) = true", raw)
		}
	}
}

func TestWantraceAssetRequestRetriesOneUnauthorizedResponse(t *testing.T) {
	calls := 0
	withWantraceTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"无效的令牌"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"channels":[{"id":1,"name":"seedance海外"}]}}`))
	})
	channels, err := wantraceListChannels(context.Background(), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(channels) != 1 || channels[0].ID != 1 {
		t.Fatalf("calls = %d, channels = %#v", calls, channels)
	}
}

func TestAssetLibrariesAreMutuallyExclusive(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	actor := &model.User{ID: "admin"}
	if err := s.saveArkPrivateAssetSetting(&model.SystemSetting{Key: arkPrivateAssetSettingKey}, arkPrivateAssetSettingValue{RealEnabled: true, VirtualEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.saveWantraceAssetSetting(&model.SystemSetting{Key: wantraceAssetSettingKey}, wantraceAssetSettingValue{Enabled: true, RealEnabled: true, APIKey: "test-key", ChannelID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.disableArkPortraitLibrariesForWantrace(actor); err != nil {
		t.Fatal(err)
	}
	_, domestic, err := s.readArkPrivateAssetSetting()
	if err != nil {
		t.Fatal(err)
	}
	if domestic.RealEnabled || domestic.VirtualEnabled {
		t.Fatalf("domestic libraries = %#v, want disabled", domestic)
	}

	if err := s.saveArkPrivateAssetSetting(&model.SystemSetting{Key: arkPrivateAssetSettingKey}, arkPrivateAssetSettingValue{RealEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.saveWantraceAssetSetting(&model.SystemSetting{Key: wantraceAssetSettingKey}, wantraceAssetSettingValue{Enabled: true, RealEnabled: true, APIKey: "test-key", ChannelID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.disableWantraceLibraryForArkPortrait(actor); err != nil {
		t.Fatal(err)
	}
	_, overseas, err := s.readWantraceAssetSetting()
	if err != nil {
		t.Fatal(err)
	}
	if overseas.Enabled || overseas.RealEnabled {
		t.Fatalf("overseas setting = %#v, want disabled", overseas)
	}
}

func TestPrepareWantraceAssetReferencesResolvesCertifiedResource(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.WantraceAsset{
		ID: "wantrace-asset-1", UserID: "user", ResourceID: "resource-1",
		AssetURI: "asset://asset-20261111-1", AssetType: "Image", Status: "Active",
	}).Error; err != nil {
		t.Fatal(err)
	}
	input := &canvasGenerationInput{ReferenceImages: []providerMedia{{StorageKey: "resource:resource-1"}}}
	if err := s.prepareWantraceAssetReferences("user", input); err != nil {
		t.Fatal(err)
	}
	if got := input.ReferenceImages[0].URL; got != "asset://asset-20261111-1" {
		t.Fatalf("reference URL = %q", got)
	}
}

func TestPrepareWantraceAssetReferencesLeavesUncertifiedResource(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	input := &canvasGenerationInput{ReferenceImages: []providerMedia{{StorageKey: "resource:not-certified"}}}
	if err := s.prepareWantraceAssetReferences("user", input); err != nil {
		t.Fatal(err)
	}
	if got := input.ReferenceImages[0].URL; got != "" {
		t.Fatalf("uncertified reference URL = %q, want empty", got)
	}
}
