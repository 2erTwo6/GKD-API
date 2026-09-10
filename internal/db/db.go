package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Open(path string) (*gorm.DB, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	g, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := g.AutoMigrate(&Admin{}, &APIKey{}, &VirtualModel{}, &RealModel{}, &RequestLog{}, &Setting{}); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	return g, nil
}

func GetSetting(g *gorm.DB, key string) (string, bool) {
	var s Setting
	err := g.WithContext(context.Background()).Where("`key` = ?", key).First(&s).Error
	if err != nil {
		return "", false
	}
	return s.Value, true
}

func SetSetting(g *gorm.DB, key, value string) error {
	return g.Where("`key` = ?", key).Assign(Setting{Key: key, Value: value}).FirstOrCreate(&Setting{}).Error
}

func Bootstrap(g *gorm.DB, adminPassword string) error {
	ctx := context.Background()
	if _, ok := GetSetting(g, "jwt_secret"); !ok {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("generate jwt secret: %w", err)
		}
		if err := SetSetting(g, "jwt_secret", hex.EncodeToString(b)); err != nil {
			return err
		}
	}
	var count int64
	g.WithContext(ctx).Model(&Admin{}).Count(&count)
	if count > 0 {
		return nil
	}
	pass := adminPassword
	source := "环境变量 GKD_ADMIN_PASSWORD"
	if pass == "" {
		p, err := randomPassword(12)
		if err != nil {
			return err
		}
		pass = p
		source = "随机生成，仅首次打印"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := g.Create(&Admin{Username: "admin", PasswordHash: string(hash)}).Error; err != nil {
		return err
	}
	log.Println("========================================")
	log.Println("首次启动，已创建管理员账号")
	log.Printf("用户名: admin")
	log.Printf("初始密码: %s (%s)", pass, source)
	log.Println("请尽快登录管理页面修改密码")
	log.Println("========================================")
	return nil
}

func randomPassword(n int) (string, error) {
	const chars = "abcdefghjkmnpqrstuvwxyz23456789"
	out := make([]byte, n)
	for i := range out {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		out[i] = chars[v.Int64()]
	}
	return string(out), nil
}
