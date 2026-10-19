package app

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

// Optional cross-repository contract: the real Go signer/client calls the real
// PHP authenticator/ledger with an isolated SQLite database, never a live site.
func TestSharedWalletPHPContract(t *testing.T) {
	fixture := os.Getenv("CANVAS_WALLET_PHP_FIXTURE")
	if fixture == "" {
		t.Skip("set CANVAS_WALLET_PHP_FIXTURE to ai_cloooud/tests/fixtures/shared_wallet_http_router.php")
	}
	fixture, err := filepath.Abs(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(fixture); err != nil {
		t.Fatal(err)
	}
	php := os.Getenv("SHARED_WALLET_PHP")
	if php == "" {
		php, err = exec.LookPath("php")
		if err != nil {
			t.Fatal("PHP is required when CANVAS_WALLET_PHP_FIXTURE is set")
		}
	}
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	dir := t.TempDir()
	database := filepath.Join(dir, "wallet.sqlite")
	db, err := gorm.Open(sqlite.Open(database), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		`CREATE TABLE site(site_id INTEGER PRIMARY KEY,status INTEGER,expire_time INTEGER)`,
		`CREATE TABLE member(member_id INTEGER PRIMARY KEY,site_id INTEGER,point INTEGER,status INTEGER,delete_time INTEGER)`,
		`CREATE TABLE member_account_log(id INTEGER PRIMARY KEY AUTOINCREMENT,site_id INTEGER,member_id INTEGER,account_data INTEGER,from_type TEXT,related_id TEXT)`,
		`CREATE TABLE hthot_wallet_txn(id INTEGER PRIMARY KEY AUTOINCREMENT,source TEXT,site_id INTEGER,member_id INTEGER,trade_no TEXT,method_key TEXT,context TEXT,initial_point INTEGER,reserved_point INTEGER,charged_point INTEGER,refunded_point INTEGER,status TEXT,log_id INTEGER,create_time INTEGER,update_time INTEGER,UNIQUE(source,site_id,trade_no))`,
		`CREATE TABLE hthot_wallet_operation(id INTEGER PRIMARY KEY AUTOINCREMENT,source TEXT,site_id INTEGER,member_id INTEGER,trade_no TEXT,operation_id TEXT,request_hash TEXT,result TEXT,create_time INTEGER,UNIQUE(source,site_id,operation_id))`,
		`CREATE TABLE hthot_wallet_nonce(nonce_hash TEXT PRIMARY KEY,expires_at INTEGER)`,
		`INSERT INTO site VALUES(7,1,0)`,
		`INSERT INTO member VALUES(11,7,1000,1,0)`,
	} {
		if err = db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	log, err := os.Create(filepath.Join(dir, "php.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	command := exec.Command(php, "-S", address, fixture)
	command.Env = append(os.Environ(), "SHARED_WALLET_TEST_DB="+database)
	command.Stdout, command.Stderr = log, log
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	ready := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		connection, dialErr := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			ready = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !ready {
		output, _ := os.ReadFile(log.Name())
		t.Fatalf("PHP fixture did not start: %s", output)
	}
	svc := &Service{dataDir: t.TempDir()}
	cipher, err := svc.encryptSettingSecret(strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.SharedWalletSettings{Mode: "external", BaseURL: "http://" + address, ClientID: "test-canvas", SiteID: 7, Issuer: "https://identity.test", SecretCipher: cipher}
	ctx := context.Background()
	call := func(action, operation string, point int64) walletReply {
		t.Helper()
		result, requestErr := svc.walletRequest(ctx, cfg, action, map[string]any{"site_id": 7, "member_id": 11, "trade_no": "go-php-contract", "operation_id": operation, "point": point, "method_key": "video", "memo": "Go与PHP跨语言账单"})
		if requestErr != nil {
			t.Fatalf("%s: %v", action, requestErr)
		}
		return result
	}
	if result := call("balance", "", 0); result.Balance != 1000 {
		t.Fatalf("initial balance: %+v", result)
	}
	for _, check := range []struct {
		action, operation string
		point, balance    int64
	}{
		{"reserve", "go-reserve", 100, 900},
		{"reserve", "go-reserve", 100, 900},
		{"supplement", "go-more", 50, 850},
		{"settle", "go-settle", 130, 870},
		{"refund", "go-refund", 0, 1000},
		{"refund", "go-refund", 0, 1000},
		{"restore", "go-restore", 130, 870},
	} {
		if result := call(check.action, check.operation, check.point); result.Balance != check.balance {
			t.Fatalf("%s balance: %+v", check.action, result)
		}
	}
	if result := call("status", "", 0); result.Status != "settled" || result.Charged != 130 {
		t.Fatalf("final receipt: %+v", result)
	}
	var entries int64
	db.Table("member_account_log").Count(&entries)
	if entries != 5 {
		t.Fatalf("duplicate ledger writes: %d", entries)
	}
	// A valid rejection may omit balance. In particular, status not_found must
	// remain distinguishable from an outage so a never-committed reserve can retry.
	_, err = svc.walletRequest(ctx, cfg, "status", map[string]any{"site_id": 7, "member_id": 11, "trade_no": "missing"})
	var business *walletBusinessError
	if !errors.As(err, &business) || business.Reason != "not_found" {
		t.Fatalf("missing order must be a business rejection, got %v", err)
	}
}
