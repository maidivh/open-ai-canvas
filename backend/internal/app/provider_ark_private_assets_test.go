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

func TestArkPrivateAssetUsesRegionalArkControlPlane(t *testing.T) {
	value, err := arkPrivateAssetControlPlaneEndpoint("", "test-region")
	if err != nil || value != "https://ark.test-region.volcengineapi.com" {
		t.Fatalf("arkPrivateAssetControlPlaneEndpoint() = %q, %v", value, err)
	}
	value, err = arkPrivateAssetControlPlaneEndpoint(arkSiteBytePlus, "AP-Southeast-1")
	if err != nil || value != "https://ark.ap-southeast-1.byteplusapi.com" {
		t.Fatalf("BytePlus control plane = %q, %v", value, err)
	}
	value, err = arkPrivateAssetControlPlaneEndpoint(arkSiteVideoClaw, "")
	if err != nil || value != "https://open.feeling.ltd/api/v1/private/sd2/assets" {
		t.Fatalf("Video Claw control plane = %q, %v", value, err)
	}
	value, err = arkPrivateAssetControlPlaneEndpoint(arkSiteWantrace, "")
	if err != nil || value != "https://api.wantrace.com/api/v1/private/sd2/assets" {
		t.Fatalf("Wantrace control plane = %q, %v", value, err)
	}
	for _, input := range [][2]string{{"aws", "cn-beijing"}, {arkSiteBytePlus, "ap.southeast"}, {arkSiteVolcengine, ""}} {
		if _, err := arkPrivateAssetControlPlaneEndpoint(input[0], input[1]); err == nil {
			t.Fatalf("%v should be rejected", input)
		}
	}
}

func TestArkAssetProviderFromBaseURL(t *testing.T) {
	cases := []struct {
		baseURL string
		want    string
	}{
		{"https://open.feeling.ltd/api/v3", arkSiteVideoClaw},
		{"https://api.wantrace.com/api/v3", arkSiteWantrace},
		{"https://ark.cn-beijing.volces.com/api/v3", arkSiteVolcengine},
		{"https://ark.cn-beijing.volcengineapi.com", arkSiteVolcengine},
		{"https://ark.ap-southeast.bytepluses.com/api/v3", arkSiteBytePlus},
		{"https://ark.ap-southeast-1.byteplusapi.com", arkSiteBytePlus},
		{"https://proxy.example.com/api/v3", ""},
		{"", ""},
	}
	for _, item := range cases {
		if got := arkAssetProviderFromBaseURL(item.baseURL); got != item.want {
			t.Fatalf("arkAssetProviderFromBaseURL(%q) = %q, want %q", item.baseURL, got, item.want)
		}
	}
}

func TestCallArkPrivateAssetAPISignsAndUsesAssetContract(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
		}
		if request.URL.Query().Get("Action") != "CreateAsset" || request.URL.Query().Get("Version") != arkPrivateAssetAPIVersion {
			t.Errorf("query = %s", request.URL.RawQuery)
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "HMAC-SHA256") {
			t.Errorf("missing Volcengine request signature")
		}
		var body map[string]interface{}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["ProjectName"] != "project-test" || body["AssetType"] != "Image" || body["GroupId"] != "group-test" {
			t.Errorf("body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Result":{"AssetId":"asset-test"}}`))
	}))
	defer server.Close()

	previousBaseURL := arkPrivateAssetAPIBaseURLOverride
	arkPrivateAssetAPIBaseURLOverride = server.URL
	t.Cleanup(func() { arkPrivateAssetAPIBaseURLOverride = previousBaseURL })

	response, err := callArkPrivateAssetAPI(context.Background(), arkPrivateAssetSettingValue{
		Region:          "test-region",
		ProjectName:     "project-test",
		AccessKeyID:     "test-access-key",
		AccessKeySecret: "test-secret-key",
	}, "CreateAsset", map[string]interface{}{
		"GroupId": "group-test", "AssetType": "Image", "URL": "https://media.example.test/reference.png", "ProjectName": "project-test",
	})
	if err != nil {
		t.Fatalf("callArkPrivateAssetAPI() error = %v", err)
	}
	if got := arkPrivateAssetResponseField(response, "AssetId"); got != "asset-test" {
		t.Fatalf("asset ID = %q", got)
	}
}

func TestCallArkPrivateAssetAPIUsesIDForGetAsset(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if request.URL.Query().Get("Action") != "GetAsset" || body["Id"] != "asset-test" || body["AssetId"] != nil {
			t.Errorf("GetAsset request = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Result":{"Status":"Processing"}}`))
	}))
	defer server.Close()

	previousBaseURL := arkPrivateAssetAPIBaseURLOverride
	arkPrivateAssetAPIBaseURLOverride = server.URL
	t.Cleanup(func() { arkPrivateAssetAPIBaseURLOverride = previousBaseURL })

	_, err := callArkPrivateAssetAPI(context.Background(), arkPrivateAssetSettingValue{
		Region: "test-region", AccessKeyID: "test-access-key", AccessKeySecret: "test-secret-key",
	}, "GetAsset", map[string]interface{}{"Id": "asset-test", "ProjectName": "project-test"})
	if err != nil {
		t.Fatalf("callArkPrivateAssetAPI() error = %v", err)
	}
}

func TestCallArkPrivateAssetAPIUsesVideoClawSigningScope(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/private/sd2/assets" {
			t.Errorf("path = %s", request.URL.Path)
		}
		if request.URL.Query().Get("Action") != "CreateAssetGroup" || request.URL.Query().Get("Version") != arkPrivateAssetAPIVersion {
			t.Errorf("query = %s", request.URL.RawQuery)
		}
		authorization := request.Header.Get("Authorization")
		if !strings.Contains(authorization, "/cn-beijing/ark/request") {
			t.Errorf("credential scope = %s", authorization)
		}
		if !strings.Contains(authorization, "SignedHeaders=content-type;host;x-content-sha256;x-date") {
			t.Errorf("signed headers = %s", authorization)
		}
		if request.Header.Get("Accept") != "application/json" {
			t.Errorf("accept header = %q", request.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Result":{"Id":"group-videoclaw"}}`))
	}))
	defer server.Close()

	previousBaseURL := arkPrivateAssetAPIBaseURLOverride
	arkPrivateAssetAPIBaseURLOverride = server.URL + "/api/v1/private/sd2/assets"
	t.Cleanup(func() { arkPrivateAssetAPIBaseURLOverride = previousBaseURL })

	response, err := callArkPrivateAssetAPI(context.Background(), arkPrivateAssetSettingValue{
		Site:            arkSiteVideoClaw,
		Region:          arkSiteVideoClawRegion,
		ProjectName:     "default",
		AccessKeyID:     "test-access-key",
		AccessKeySecret: "test-secret-key",
	}, "CreateAssetGroup", map[string]interface{}{
		"Name": "seedance-assets", "GroupType": "AIGC", "ProjectName": "default",
	})
	if err != nil {
		t.Fatalf("callArkPrivateAssetAPI() error = %v", err)
	}
	if got := arkPrivateAssetResponseField(response, "Id"); got != "group-videoclaw" {
		t.Fatalf("group ID = %q", got)
	}
}

func TestArkPrivateAssetControlPlaneDisablesGenerationAnalytics(t *testing.T) {
	ctx := context.WithValue(context.Background(), providerAnalyticsKey{}, providerAnalyticsContext{Service: &Service{}, Capability: "video"})
	ctx = withoutProviderAnalytics(ctx)
	metadata, ok := ctx.Value(providerAnalyticsKey{}).(providerAnalyticsContext)
	if !ok || metadata.Service != nil || metadata.Capability != "" {
		t.Fatalf("provider analytics = %#v, %v", metadata, ok)
	}
}

func TestArkPrivateAssetAutomaticSyncIsOptionalWhenSettingDisabled(t *testing.T) {
	enabled, err := arkPrivateAssetAutomaticSyncEnabled(arkPrivateAssetSettingValue{})
	if err != nil || enabled {
		t.Fatalf("arkPrivateAssetAutomaticSyncEnabled() = %v, %v; want false, nil", enabled, err)
	}

	_, err = arkPrivateAssetAutomaticSyncEnabled(arkPrivateAssetSettingValue{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "尚未配置") {
		t.Fatalf("enabled incomplete setting error = %v, want configuration error", err)
	}
}

func TestArkPrivateAssetSettingsEncryptSecret(t *testing.T) {
	svc := &Service{dataDir: t.TempDir()}
	value, err := arkPrivateAssetSettingFromRequest(ArkPrivateAssetSettingRequest{
		Enabled: true, Region: "test-region", ProjectName: "project-test", AccessKeyID: "test-access-key", AccessKeySecret: "test-secret-key",
	}, defaultArkPrivateAssetSetting())
	if err != nil {
		t.Fatalf("arkPrivateAssetSettingFromRequest() error = %v", err)
	}
	ciphertext, err := svc.encryptSettingSecret(value.AccessKeySecret)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ciphertext, encryptedSettingPrefix) || strings.Contains(ciphertext, value.AccessKeySecret) {
		t.Fatalf("encrypted secret = %q", ciphertext)
	}
	plaintext, err := svc.decryptSettingSecret(ciphertext)
	if err != nil || plaintext != value.AccessKeySecret {
		t.Fatalf("decryptSettingSecret() = %q, %v", plaintext, err)
	}
	public := publicArkPrivateAssetSetting(nil, value)
	if !public.HasAccessKeySecret || public.AccessKeyID != "test-access-key" {
		t.Fatalf("public setting = %#v", public)
	}
}

func TestArkPrivateAssetResponseFieldReadsNestedAssetStatus(t *testing.T) {
	response := map[string]interface{}{
		"Result": map[string]interface{}{
			"Asset": map[string]interface{}{"Status": "Active", "AssetId": "asset-nested"},
		},
	}
	if got := arkPrivateAssetResponseField(response, "Status"); got != "Active" {
		t.Fatalf("status = %q", got)
	}
	if got := arkPrivateAssetResponseField(response, "AssetId"); got != "asset-nested" {
		t.Fatalf("asset ID = %q", got)
	}
}

func TestArkPrivateAssetResponseFieldReadsCreateGroupID(t *testing.T) {
	response := map[string]interface{}{
		"Result": map[string]interface{}{
			"Group": map[string]interface{}{"Id": "group-created"},
		},
	}
	if got := arkPrivateAssetResponseField(response, "GroupId", "Id"); got != "group-created" {
		t.Fatalf("group ID = %q", got)
	}
}

func TestShouldRetryArkPrivateAssetBinding(t *testing.T) {
	cases := []struct {
		name    string
		binding *model.ArkPrivateAssetBinding
		want    bool
	}{
		{
			name:    "素材组未建成时网络失败可重试",
			binding: &model.ArkPrivateAssetBinding{Status: arkPrivateAssetStatusFail, Error: `创建方舟素材组失败：Post "https://ark.cn-shanghai.volcengineapi.com/": EOF`},
			want:    true,
		},
		{
			name:    "素材组未建成时上游业务拒绝也可重试",
			binding: &model.ArkPrivateAssetBinding{Status: arkPrivateAssetStatusFail, Error: "创建方舟素材组失败：方舟素材库请求失败（HTTP 403）：SubscriptionRequired"},
			want:    true,
		},
		{
			name:    "素材组已建时上传素材失败可重试",
			binding: &model.ArkPrivateAssetBinding{Status: arkPrivateAssetStatusFail, AssetGroupID: "group-1", Error: "上传方舟可信素材失败：Post \"https://ark.cn-beijing.volcengineapi.com/\": EOF"},
			want:    true,
		},
		{
			name:    "素材组已建时素材 ID 解析失败可重试",
			binding: &model.ArkPrivateAssetBinding{Status: arkPrivateAssetStatusFail, AssetGroupID: "group-1", Error: "方舟素材库没有返回素材 ID"},
			want:    true,
		},
		{
			name:    "审核拒绝保持终态不重试",
			binding: &model.ArkPrivateAssetBinding{Status: arkPrivateAssetStatusFail, AssetGroupID: "group-1", ArkAssetID: "asset-1", Error: "FaceMismatch: Face consistency verification failed."},
			want:    false,
		},
		{
			name:    "素材组已建时的其他失败不重试",
			binding: &model.ArkPrivateAssetBinding{Status: arkPrivateAssetStatusFail, AssetGroupID: "group-1", Error: "生成方舟素材临时地址失败：对象存储不可用"},
			want:    false,
		},
		{
			name:    "非失败状态不重试",
			binding: &model.ArkPrivateAssetBinding{Status: arkPrivateAssetStatusLive, ArkAssetID: "asset-1"},
			want:    false,
		},
	}
	for _, item := range cases {
		if got := shouldRetryArkPrivateAssetBinding(item.binding); got != item.want {
			t.Fatalf("%s: shouldRetryArkPrivateAssetBinding() = %v, want %v", item.name, got, item.want)
		}
	}
	if shouldRetryArkPrivateAssetBinding(nil) {
		t.Fatal("shouldRetryArkPrivateAssetBinding(nil) = true, want false")
	}
}

func TestCallArkPrivateAssetAPIPassesThroughUpstreamHTTPErrors(t *testing.T) {
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"SubscriptionRequired","Message":"This API requires an active subscription."}}}`))
	}))
	defer server.Close()

	previousBaseURL := arkPrivateAssetAPIBaseURLOverride
	arkPrivateAssetAPIBaseURLOverride = server.URL
	t.Cleanup(func() { arkPrivateAssetAPIBaseURLOverride = previousBaseURL })

	_, err := callArkPrivateAssetAPI(context.Background(), arkPrivateAssetSettingValue{
		Region: "test-region", AccessKeyID: "test-access-key", AccessKeySecret: "test-secret-key",
	}, "CreateAssetGroup", map[string]interface{}{"ProjectName": "project-test"})
	if err == nil {
		t.Fatal("callArkPrivateAssetAPI() error = nil, want upstream passthrough error")
	}
	for _, want := range []string{"403", "SubscriptionRequired", "This API requires an active subscription."} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("callArkPrivateAssetAPI() error = %q, want contains %q", err.Error(), want)
		}
	}
}

func TestArkPrivateAssetUpstreamDetail(t *testing.T) {
	if got := arkPrivateAssetUpstreamDetail(map[string]interface{}{}); got != "" {
		t.Fatalf("empty response detail = %q, want empty", got)
	}
	response := map[string]interface{}{
		"ResponseMetadata": map[string]interface{}{
			"Error": map[string]interface{}{"Code": "FaceMismatch", "Message": "Face consistency verification failed."},
		},
	}
	if got := arkPrivateAssetUpstreamDetail(response); got != "FaceMismatch Face consistency verification failed." {
		t.Fatalf("detail = %q", got)
	}
	emptyError := map[string]interface{}{"ResponseMetadata": map[string]interface{}{"Error": map[string]interface{}{}}}
	if got := arkPrivateAssetUpstreamDetail(emptyError); got != "方舟素材库请求失败" {
		t.Fatalf("empty upstream error detail = %q", got)
	}
}
