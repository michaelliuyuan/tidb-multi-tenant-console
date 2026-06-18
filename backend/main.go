package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"

	"github.com/tidb-multi-tenant/console/internal/api"
	"github.com/tidb-multi-tenant/console/internal/model"
	"github.com/tidb-multi-tenant/console/internal/store"
)

type Config struct {
	Server struct {
		Addr string `yaml:"addr"`
	} `yaml:"server"`
	Metadata struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		DB       string `yaml:"db"`
	} `yaml:"metadata"`
	Clusters []model.Cluster `yaml:"clusters"`
}

func main() {
	cfg := loadConfig("config.yaml")

	// 1) 连元数据库
	db, err := store.OpenTiDB(cfg.Metadata.Host, cfg.Metadata.Port, cfg.Metadata.User, cfg.Metadata.Password, cfg.Metadata.DB)
	if err != nil {
		log.Fatalf("connect metadata db: %v", err)
	}
	meta := &store.Metadata{DB: db}

	// 2) 运行迁移（mt_console schema）
	if err := runMigrations(meta); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	// 3) 预注册配置中的集群
	for _, c := range cfg.Clusters {
		if c.Status == "" {
			c.Status = "active"
		}
		if _, err := meta.UpsertCluster(c, c.Password); err != nil {
			log.Printf("register cluster %s: %v", c.Name, err)
		} else {
			log.Printf("registered cluster %s", c.Name)
		}
	}

	// 4) 启动 HTTP
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	a := api.New(meta)
	a.Register(r)

	// 5) 静态文件服务：优先读 ../frontend/dist，不存在则读 frontend/dist
	staticRoot := "../frontend/dist"
	if _, err := os.Stat(staticRoot); os.IsNotExist(err) {
		staticRoot = "frontend/dist"
	}
	if _, err := os.Stat(staticRoot); err == nil {
		// SPA fallback：非 /api 请求且非静态文件则返回 index.html
		r.NoRoute(func(c *gin.Context) {
			path := c.Request.URL.Path
			if len(path) >= 4 && path[:4] == "/api" {
				c.Status(404)
				return
			}
			// 尝试直接返回静态文件
			fp := filepath.Join(staticRoot, path)
			if _, err := os.Stat(fp); err == nil {
				c.File(fp)
				return
			}
			c.File(filepath.Join(staticRoot, "index.html"))
		})
		// 也注册 assets 目录
		assetsDir := filepath.Join(staticRoot, "assets")
		if _, err := os.Stat(assetsDir); err == nil {
			r.Static("/assets", assetsDir)
		}
		log.Printf("serving static files from %s", staticRoot)
	} else {
		log.Printf("static root %s not found, API-only mode", staticRoot)
	}

	addr := cfg.Server.Addr
	if addr == "" {
		addr = ":8088"
	}
	log.Printf("TiDB 多租户管控台 listening on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}

func loadConfig(path string) *Config {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read config %s: %v (copy config.yaml and edit)", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		log.Fatalf("parse config: %v", err)
	}
	return &c
}

// runMigrations 执行 migrations/ 下的 SQL（P0 简化：单文件）。
func runMigrations(meta *store.Metadata) error {
	sqlBytes, err := os.ReadFile("migrations/0001_init.sql")
	if err != nil {
		return err
	}
	if _, err := meta.DB.Exec(string(sqlBytes)); err != nil {
		return err
	}
	return nil
}
