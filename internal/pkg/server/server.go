package server

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
)

//cors
func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		method := c.Request.Method
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "Content-Type, TraceID, IsTest, Token, TimeOut, Authorization")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, DELETE")
		c.Header("Access-Control-Expose-Headers", "Content-Length, Access-Control-Allow-Origin, Access-Control-Allow-Headers, Content-Type")
		c.Header("Access-Control-Allow-Credentials", "true")
		if method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
		}
		c.Next()
	}
}

type (
	Server interface {
		GrpcServer() *grpc.Server

		HttpServer() *gin.Engine

		ListenHttpAndGrpcServe(port string) error
	}

	Option struct {
		f func(*serverImpl)
	}

	serverImpl struct {
		grpcServer *grpc.Server
		httpServer *gin.Engine
	}
)

//set grpc server
func GrpcServer(grpcServer *grpc.Server) Option {
	return Option{func(s *serverImpl) {
		s.grpcServer = grpcServer
	}}
}

//set http server
func HttpServer(httpServer *gin.Engine) Option {
	return Option{func(s *serverImpl) {
		s.httpServer = httpServer
	}}
}

func NewServer(options ...Option) Server {
	gin.SetMode(gin.ReleaseMode)
	gin.Logger()
	//init
	s := new(serverImpl)

	//option
	for _, option := range options {
		option.f(s)
	}

	if s.grpcServer == nil {
		s.grpcServer = grpc.NewServer()
	}
	if s.httpServer == nil {
		s.httpServer = gin.New()
	}
	return s
}

func (s *serverImpl) GrpcServer() *grpc.Server {
	return s.grpcServer
}

func (s *serverImpl) HttpServer() *gin.Engine {
	return s.httpServer
}

func (s *serverImpl) ListenHttpAndGrpcServe(port string) error {
	return http.ListenAndServe(port, h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.Contains(r.Header.Get("Content-Type"), "application/grpc") {
			s.grpcServer.ServeHTTP(w, r)
		} else {
			s.httpServer.ServeHTTP(w, r)
		}
	}), &http2.Server{}))
}
