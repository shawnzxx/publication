//go:build integration

package service_test

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/bigwhite/shortlink/pkg/repository/postgres"
	redis_repo "github.com/bigwhite/shortlink/pkg/repository/redis"
	"github.com/bigwhite/shortlink/pkg/service"
)

var (
	dbPool      *sql.DB
	redisClient *redis.Client
	testService *service.ShortenerService
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	// 1. 定义网络名称并创建共享网络
	networkName := "shortlink-test-network"
	network, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{Name: networkName},
	})
	if err != nil {
		log.Fatalf("无法创建共享网络: %s", err)
	}
	defer network.Remove(ctx)

	// 2. 并行启动 PostgreSQL 和 Redis 容器
	type containerResult struct {
		container testcontainers.Container
		err       error
	}

	pgChan := make(chan containerResult, 1)
	redisChan := make(chan containerResult, 1)

	// 启动 PostgreSQL 容器
	go func() {
		container, err := setupPostgres(ctx, networkName)
		pgChan <- containerResult{container: container, err: err}
	}()

	// 启动 Redis 容器
	go func() {
		container, err := setupRedis(ctx, networkName)
		redisChan <- containerResult{container: container, err: err}
	}()

	// 等待两个容器都启动完成并获取结果
	pgResult := <-pgChan
	redisResult := <-redisChan

	// 检查错误并设置 defer
	if pgResult.err != nil {
		log.Fatalf("PostgreSQL 容器启动失败: %v", pgResult.err)
	}
	pgContainer := pgResult.container
	defer pgContainer.Terminate(ctx)

	if redisResult.err != nil {
		log.Fatalf("Redis 容器启动失败: %v", redisResult.err)
	}
	redisContainer := redisResult.container
	defer redisContainer.Terminate(ctx)

	// ... (后续的连接和初始化逻辑完全不变)
	pgHost, _ := pgContainer.Host(ctx)
	pgPort, _ := pgContainer.MappedPort(ctx, "5432")
	dsn := fmt.Sprintf("postgres://test:password@%s:%s/testdb?sslmode=disable", pgHost, pgPort.Port())
	pool, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("无法连接 PG: %s", err)
	}
	dbPool = pool
	defer dbPool.Close()

	redisHost, _ := redisContainer.Host(ctx)
	redisPort, _ := redisContainer.MappedPort(ctx, "6379")
	redisAddr := fmt.Sprintf("%s:%s", redisHost, redisPort.Port())
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("无法连接 Redis: %s", err)
	}
	redisClient = rdb
	defer redisClient.Close()

	migrator, _ := migrate.New("file://../../migrations", dsn)
	if err := migrator.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("无法执行 migration: %s", err)
	}

	linkRepo := postgres.NewPgLinkRepository(dbPool)
	linkCache := redis_repo.NewRedisLinkCache(redisClient)
	testService = service.NewShortenerService(linkRepo, linkCache)

	exitCode := m.Run()
	os.Exit(exitCode)
}

// setupPostgres 接收网络名称字符串，而不是 network 对象
func setupPostgres(ctx context.Context, networkName string) (testcontainers.Container, error) {
	req := testcontainers.ContainerRequest{
		Image:          "postgres:18-alpine3.22",
		ExposedPorts:   []string{"5432/tcp"},
		Env:            map[string]string{"POSTGRES_USER": "test", "POSTGRES_PASSWORD": "password", "POSTGRES_DB": "testdb"},
		WaitingFor:     wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		Networks:       []string{networkName},
		NetworkAliases: map[string][]string{networkName: {"postgres-db"}},
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		return nil, fmt.Errorf("PG 启动失败: %w", err)
	}
	return container, nil
}

// setupRedis 接收网络名称字符串，而不是 network 对象
func setupRedis(ctx context.Context, networkName string) (testcontainers.Container, error) {
	req := testcontainers.ContainerRequest{
		Image:          "redis:7-alpine",
		ExposedPorts:   []string{"6379/tcp"},
		WaitingFor:     wait.ForLog("Ready to accept connections"),
		Networks:       []string{networkName},
		NetworkAliases: map[string][]string{networkName: {"redis-cache"}},
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		return nil, fmt.Errorf("Redis 启动失败: %w", err)
	}
	return container, nil
}

// --- TestCreateAndRedirect_HappyPath 函数及其辅助函数 assertEventually 完全不变 ---

func TestCreateAndRedirect_HappyPath(t *testing.T) {
	ctx := context.Background()
	originalURL := "https://www.google.com/very-long-path"

	tx, err := dbPool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	txRepo := postgres.NewPgLinkRepository(tx)
	txTestService := service.NewShortenerService(txRepo, redis_repo.NewRedisLinkCache(redisClient))

	createdLink, err := txTestService.CreateLink(ctx, originalURL)
	if err != nil {
		t.Fatalf("CreateLink 不应返回错误: %v", err)
	}

	redirectedLink, err := txTestService.Redirect(ctx, createdLink.ShortCode)
	if err != nil {
		t.Fatalf("Redirect 不应返回错误: %v", err)
	}
	if redirectedLink == nil || redirectedLink.OriginalURL != originalURL {
		t.Fatalf("重定向的链接不正确")
	}

	assertEventually(t, func() bool {
		count, err := redisClient.Get(ctx, fmt.Sprintf("link:visits:%s", createdLink.ShortCode)).Int64()
		if err != nil {
			return false
		}
		return count == 1
	}, "访问计数应该在 Redis 中变为 1")

	_, err = txTestService.Redirect(ctx, createdLink.ShortCode)
	if err != nil {
		t.Fatalf("第二次 Redirect 不应返回错误: %v", err)
	}

	assertEventually(t, func() bool {
		count, err := redisClient.Get(ctx, fmt.Sprintf("link:visits:%s", createdLink.ShortCode)).Int64()
		if err != nil {
			return false
		}
		return count == 2
	}, "再次访问后，计数应该在 Redis 中变为 2")

	redisClient.Del(ctx, fmt.Sprintf("link:visits:%s", createdLink.ShortCode))
}

// AssertEventuallyOptions 用于配置 assertEventually 的行为
type AssertEventuallyOptions struct {
	Timeout  time.Duration // 超时时间，默认为 2 秒
	Interval time.Duration // 检查间隔，默认为 50 毫秒
}

// assertEventually 等待条件满足，使用默认超时和间隔（向后兼容）
// 默认超时为 2 秒，默认间隔为 50 毫秒
// 示例：
//
//	assertEventually(t, func() bool { return someCondition() }, "条件应该满足")
func assertEventually(t *testing.T, condition func() bool, msgAndArgs ...interface{}) {
	assertEventuallyWithOptions(t, condition, nil, msgAndArgs...)
}

// assertEventuallyWithOptions 等待条件满足，允许自定义超时和间隔
// 如果 options 为 nil 或 Timeout/Interval 为 0，则使用默认值
// 示例：
//
//	// 自定义超时为 5 秒
//	assertEventuallyWithOptions(t, func() bool { return someCondition() },
//	    &AssertEventuallyOptions{Timeout: 5 * time.Second}, "条件应该满足")
//	// 自定义超时和间隔
//	assertEventuallyWithOptions(t, func() bool { return someCondition() },
//	    &AssertEventuallyOptions{Timeout: 5 * time.Second, Interval: 100 * time.Millisecond}, "条件应该满足")
func assertEventuallyWithOptions(t *testing.T, condition func() bool, options *AssertEventuallyOptions, msgAndArgs ...interface{}) {
	t.Helper()

	const (
		defaultTimeout  = 2 * time.Second
		defaultInterval = 50 * time.Millisecond
	)

	timeout := defaultTimeout
	interval := defaultInterval

	if options != nil {
		if options.Timeout > 0 {
			timeout = options.Timeout
		}
		if options.Interval > 0 {
			interval = options.Interval
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			t.Fatalf("Condition was not met within %v: %s", timeout, fmt.Sprint(msgAndArgs...))
		case <-ticker.C:
			if condition() {
				return
			}
		}
	}
}
