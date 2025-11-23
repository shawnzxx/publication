//go:build unit

package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bigwhite/shortlink/pkg/domain"
	"github.com/bigwhite/shortlink/pkg/handler"
)

// BaseStubLinkService 是一个基础的 Stub，实现 LinkService 接口的所有方法
// 这样做的好处是：一旦接口增加方法，只需在这里添加，所有依赖它的 Stub 自动获得默认实现
type BaseStubLinkService struct{}

func (b *BaseStubLinkService) CreateLink(ctx context.Context, originalURL string) (*domain.Link, error) {
	return nil, errors.New("CreateLink not implemented")
}

func (b *BaseStubLinkService) Redirect(ctx context.Context, code string) (*domain.Link, error) {
	return nil, errors.New("Redirect not implemented")
}

func (b *BaseStubLinkService) GetStats(ctx context.Context, code string) (int64, error) {
	return 0, errors.New("GetStats not implemented")
}

// 现在，测试 Stub 可以嵌入 BaseStubLinkService，只需覆盖需要的方法
type OverwriteStubLinkService struct {
	*BaseStubLinkService
	CreateLinkFunc func(ctx context.Context, originalURL string) (*domain.Link, error)
}

// 只覆盖需要测试的方法
func (s *OverwriteStubLinkService) CreateLink(ctx context.Context, originalURL string) (*domain.Link, error) {
	if s.CreateLinkFunc != nil {
		return s.CreateLinkFunc(ctx, originalURL)
	}
	return s.BaseStubLinkService.CreateLink(ctx, originalURL)
}

func TestLinkHandler_OverwriteCreateLink(t *testing.T) {
	// 2. 使用表驱动测试
	testCases := []struct {
		name           string
		reqBody        string
		stub           *OverwriteStubLinkService
		wantStatusCode int
		wantRespBody   string
	}{
		{
			name:    "成功创建",
			reqBody: `{"url": "https://example.com"}`,
			stub: &OverwriteStubLinkService{
				CreateLinkFunc: func(ctx context.Context, originalURL string) (*domain.Link, error) {
					return &domain.Link{ShortCode: "success"}, nil
				},
			},
			wantStatusCode: http.StatusCreated,
			wantRespBody:   `{"short_code":"success"}`,
		},
		{
			name:           "请求体 JSON 格式错误",
			reqBody:        `{"url": "https://example.com"`, // 缺少右括号
			stub:           &OverwriteStubLinkService{},     // 不会被调用
			wantStatusCode: http.StatusBadRequest,
			wantRespBody:   `Invalid request body`,
		},
		{
			name:    "Service 返回错误",
			reqBody: `{"url": "https://example.com"}`,
			stub: &OverwriteStubLinkService{
				CreateLinkFunc: func(ctx context.Context, originalURL string) (*domain.Link, error) {
					return nil, errors.New("internal error")
				},
			},
			wantStatusCode: http.StatusInternalServerError,
			wantRespBody:   `Failed to create link`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 3. 准备请求和响应记录器
			req := httptest.NewRequest("POST", "/api/links", strings.NewReader(tc.reqBody))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()

			// 4. 注入 Stub 并创建 Handler
			linkHandler := handler.NewLinkHandler(tc.stub)

			// 5. 执行 Handler
			http.HandlerFunc(linkHandler.CreateLink).ServeHTTP(rr, req)

			// 6. 断言结果
			if rr.Code != tc.wantStatusCode {
				t.Errorf("status code got %d, want %d", rr.Code, tc.wantStatusCode)
			}

			// 对 Body 的断言需要注意，错误响应可能包含换行符
			trimmedBody := strings.TrimSpace(rr.Body.String())

			// 如果期望是 JSON，我们可以反序列化后比较，更健壮
			if strings.HasPrefix(tc.wantRespBody, "{") {
				var got, want map[string]interface{}
				if err := json.Unmarshal([]byte(trimmedBody), &got); err != nil {
					t.Fatalf("failed to unmarshal response body: %v", err)
				}
				if err := json.Unmarshal([]byte(tc.wantRespBody), &want); err != nil {
					t.Fatalf("failed to unmarshal wantRespBody: %v", err)
				}
				// 这里可以用更完善的 deep equal 库
				if got["short_code"] != want["short_code"] {
					t.Errorf("response body got %v, want %v", got, want)
				}
			} else {
				if trimmedBody != tc.wantRespBody {
					t.Errorf("response body got %q, want %q", trimmedBody, tc.wantRespBody)
				}
			}
		})
	}
}
