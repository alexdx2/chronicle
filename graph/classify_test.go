package graph

import "testing"

func TestClassifyProviderRole(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"BattleGuard", "guard"},
		{"AuthGuard", "guard"},
		{"LoggingInterceptor", "interceptor"},
		{"TrapInterceptor", "interceptor"},
		{"ValidationPipe", "pipe"},
		{"LoggingMiddleware", "middleware"},
		{"HttpExceptionFilter", "filter"},
		{"CleanupTask", "task"},
		{"PaymentProcessor", "processor"},

		// NOT support — core providers:
		{"ArenaService", ""},
		{"TomService", ""},
		{"PrismaService", ""},
		{"JerryClient", ""},
		{"BattleResultProducer", ""},
		{"BattleResultConsumer", ""},
		{"SpectatorService", ""},

		// Gateway is NOT support — it's an API entrypoint:
		{"BattleGateway", ""},
		{"WebSocketGateway", ""},
	}
	for _, tt := range tests {
		got := classifyProviderRole(tt.name)
		if got != tt.want {
			t.Errorf("classifyProviderRole(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
