package security

import (
	"deployer-agent/config"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UniqueID    string `json:"unique_id,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Environment string `json:"environment,omitempty"`
	Type        string `json:"type,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Timestamp   int64  `json:"timestamp,omitempty"`
	jwt.RegisteredClaims
}

func CreateToken(data map[string]interface{}, expiresMinutes int) (string, error) {
	cfg := config.GetConfig()
	if expiresMinutes == 0 {
		expiresMinutes = cfg.TokenExpirationMinutes
	}

	claims := jwt.MapClaims{
		"exp": time.Now().Add(time.Duration(expiresMinutes) * time.Minute).Unix(),
	}

	for k, v := range data {
		claims[k] = v
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(cfg.APIToAgentSigningKey))
}

func VerifyToken(tokenString string) (jwt.MapClaims, error) {
	cfg := config.GetConfig()

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(cfg.APIToAgentSigningKey), nil
	})

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, fmt.Errorf("invalid token")
}
