package middleware

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	// Temp marks a short-lived token issued when an admin-created staff member logs in
	// with their generated password. The middleware rejects it so the only thing it can
	// authorize is POST /auth/change-password-first-login (which parses the JWT itself).
	Temp   bool   `json:"temp,omitempty"`
	jwt.RegisteredClaims
}

func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := ""
		if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
			raw = strings.TrimPrefix(h, "Bearer ")
		} else if q := c.Query("token"); q != "" {
			raw = q
		}
		if raw == "" {
			log.Printf("[401] missing token | ip=%s path=%s", c.ClientIP(), c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			return
		}
		token, err := jwt.ParseWithClaims(raw, &Claims{},
			func(t *jwt.Token) (any, error) { return []byte(secret), nil },
		)
		if err != nil || !token.Valid {
			log.Printf("[401] invalid token | ip=%s path=%s | %v", c.ClientIP(), c.Request.URL.Path, err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		claims := token.Claims.(*Claims)
		if claims.Temp {
			log.Printf("[401] temporary token cannot access regular API | path=%s", c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "password change required"})
			return
		}
		c.Set("user_id", claims.UserID)
		c.Set("user_role", claims.Role)
		c.Set("user_email", claims.Email)
		c.Next()
	}
}
