package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
)

type loggingInterceptor struct {
	logger *slog.Logger
}

// WrapStreamingClient implements [connect.Interceptor].
func (l loggingInterceptor) WrapStreamingClient(handler connect.StreamingClientFunc) connect.StreamingClientFunc {
	return nil
}

// WrapStreamingHandler implements [connect.Interceptor].
func (l loggingInterceptor) WrapStreamingHandler(handler connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return nil
}

// WrapUnary implements [connect.Interceptor].
func (l loggingInterceptor) WrapUnary(handler connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		start := time.Now()
		response, responseErr := handler(ctx, req)
		end := time.Now()

		latency := end.Sub(start)
		args := []any{
			"protocol", req.Peer().Protocol,
			"address", req.Peer().Addr,
			"procedure", req.Spec().Procedure,
			"latency", latency,
		}

		if responseErr == nil {
			l.logger.InfoContext(
				ctx, "request finished",
				append(args,
					"status", "ok",
				)...,
			)
			return response, nil
		}
		if connectErr := new(connect.Error); errors.As(responseErr, &connectErr) {
			args = append(args,
				"status", connectErr.Code(),
			)
		}

		l.logger.ErrorContext(
			ctx, "request finished",
			append(args,
				"status", connect.CodeUnknown,
				"error", responseErr,
			)...,
		)
		return response, responseErr
	}
}
