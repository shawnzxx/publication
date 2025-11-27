////go:build integration

package redis_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	redisc "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/bigwhite/shortlink/pkg/repository/redis"
)

var redisClient *redisc.Client

func TestMain(m *testing.M) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(5 * time.Minute),
	}
	redisContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		log.Fatalf("无法启动 Redis 容器: %s", err)
	}
	defer func() {
		if err := redisContainer.Terminate(context.Background()); err != nil {
			log.Fatalf("无法终止 Redis 容器: %s", err)
		}
	}()

	host, _ := redisContainer.Host(ctx)
	port, _ := redisContainer.MappedPort(ctx, "6379")
	addr := fmt.Sprintf("%s:%s", host, port.Port())

	// 创建 Redis 客户端并测试连接
	redisClient = redisc.NewClient(&redisc.Options{
		Addr: addr,
	})
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Fatalf("无法连接到 Redis 容器: %s", err)
	}
	defer redisClient.Close()

	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestRedisLinkCache(t *testing.T) {
	t.Run("IncrementVisitCount", func(t *testing.T) {
		ctx := context.Background()
		cache := redis.NewRedisLinkCache(redisClient)
		code := "test-code-1"

		// 清理测试数据
		defer redisClient.Del(ctx, fmt.Sprintf("link:visits:%s", code))

		// 测试递增访问计数
		err := cache.IncrementVisitCount(ctx, code)
		if err != nil {
			t.Fatalf("IncrementVisitCount() 返回了意外错误: %v", err)
		}

		// 验证计数已增加
		count, err := cache.GetVisitCount(ctx, code)
		if err != nil {
			t.Fatalf("GetVisitCount() 返回了意外错误: %v", err)
		}
		if count != 1 {
			t.Errorf("GetVisitCount() = %d, 期望 1", count)
		}

		// 再次递增
		err = cache.IncrementVisitCount(ctx, code)
		if err != nil {
			t.Fatalf("第二次 IncrementVisitCount() 返回了意外错误: %v", err)
		}

		count, err = cache.GetVisitCount(ctx, code)
		if err != nil {
			t.Fatalf("第二次 GetVisitCount() 返回了意外错误: %v", err)
		}
		if count != 2 {
			t.Errorf("GetVisitCount() = %d, 期望 2", count)
		}
	})

	t.Run("GetVisitCount_NotExists", func(t *testing.T) {
		ctx := context.Background()
		cache := redis.NewRedisLinkCache(redisClient)
		code := "non-existent-code"

		// 测试获取不存在的键
		count, err := cache.GetVisitCount(ctx, code)
		if err != nil {
			t.Fatalf("GetVisitCount() 返回了意外错误: %v", err)
		}
		if count != 0 {
			t.Errorf("GetVisitCount() = %d, 期望 0 (键不存在)", count)
		}
	})

	t.Run("MultipleCodes", func(t *testing.T) {
		ctx := context.Background()
		cache := redis.NewRedisLinkCache(redisClient)
		code1 := "test-code-2"
		code2 := "test-code-3"

		// 清理测试数据
		defer func() {
			redisClient.Del(ctx, fmt.Sprintf("link:visits:%s", code1))
			redisClient.Del(ctx, fmt.Sprintf("link:visits:%s", code2))
		}()

		// 为不同的 code 递增计数
		if err := cache.IncrementVisitCount(ctx, code1); err != nil {
			t.Fatalf("IncrementVisitCount(code1) 返回了意外错误: %v", err)
		}
		if err := cache.IncrementVisitCount(ctx, code1); err != nil {
			t.Fatalf("第二次 IncrementVisitCount(code1) 返回了意外错误: %v", err)
		}
		if err := cache.IncrementVisitCount(ctx, code2); err != nil {
			t.Fatalf("IncrementVisitCount(code2) 返回了意外错误: %v", err)
		}

		// 验证每个 code 的计数是独立的
		count1, err := cache.GetVisitCount(ctx, code1)
		if err != nil {
			t.Fatalf("GetVisitCount(code1) 返回了意外错误: %v", err)
		}
		if count1 != 2 {
			t.Errorf("GetVisitCount(code1) = %d, 期望 2", count1)
		}

		count2, err := cache.GetVisitCount(ctx, code2)
		if err != nil {
			t.Fatalf("GetVisitCount(code2) 返回了意外错误: %v", err)
		}
		if count2 != 1 {
			t.Errorf("GetVisitCount(code2) = %d, 期望 1", count2)
		}
	})
}

