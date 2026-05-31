package handler

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"smartresidency/internal/middleware"
)

// MailSender is the subset of *mail.Sender that auth needs.
// Decoupling lets tests inject a stub; nil disables email entirely.
type MailSender interface {
	SendCode(to, code string) error
}

type AuthHandler struct {
	db   *pgxpool.Pool
	mail MailSender
}

func NewAuthHandler(db *pgxpool.Pool, mail MailSender) *AuthHandler {
	return &AuthHandler{db: db, mail: mail}
}

type registerReq struct {
	Email          string `json:"email"    binding:"required,email"`
	Password       string `json:"password" binding:"required,min=6"`
	FullName       string `json:"full_name" binding:"required"`
	Phone          string `json:"phone"`
	IIN            string `json:"iin"`
	PersonType     string `json:"person_type"`
	City           string `json:"city"`
	Street         string `json:"street"`
	PropertyType   string `json:"property_type"`
	PropertyNumber string `json:"property_number"`
	FullAddress    string `json:"full_address"`
	Entrance       *int   `json:"entrance"`
	Floor          *int   `json:"floor"`
	Apartment      string `json:"apartment"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		internalError(c, "Auth.Register/bcrypt", err)
		return
	}

	userID := uuid.New().String()
	ctx := context.Background()

	_, err = h.db.Exec(ctx,
		`INSERT INTO users (id, email, password_hash) VALUES ($1, $2, $3)`,
		userID, strings.ToLower(strings.TrimSpace(req.Email)), string(hash),
	)
	if err != nil {
		if strings.Contains(err.Error(), "23505") || strings.Contains(err.Error(), "duplicate") {
			c.JSON(http.StatusConflict, gin.H{"error": "email already registered"})
			return
		}
		internalError(c, "Auth.Register/users", err)
		return
	}

	_, err = h.db.Exec(ctx, `
		INSERT INTO profiles (
			id, full_name, email, phone, iin, person_type,
			city, street, property_type, property_number, full_address,
			entrance, floor, apartment,
			role, verification_status
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'resident','not_submitted')`,
		userID, req.FullName, req.Email, req.Phone, req.IIN, req.PersonType,
		req.City, req.Street, req.PropertyType, req.PropertyNumber, req.FullAddress,
		req.Entrance, req.Floor, req.Apartment,
	)
	if err != nil {
		internalError(c, "Auth.Register/profiles", err)
		return
	}

	token, err := makeToken(userID, req.Email, "resident")
	if err != nil {
		internalError(c, "Auth.Register/token", err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"token": token, "user_id": userID})
}

type loginReq struct {
	Email    string `json:"email"    binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()
	var (
		userID, hash, role, specialty string
		mustChange                    bool
	)
	err := h.db.QueryRow(ctx, `
		SELECT u.id, u.password_hash,
		       COALESCE(p.role, 'resident'),
		       COALESCE(p.specialty, ''),
		       COALESCE(p.password_change_required, FALSE)
		FROM users u
		LEFT JOIN profiles p ON p.id = u.id
		WHERE u.email = $1`,
		strings.ToLower(strings.TrimSpace(req.Email)),
	).Scan(&userID, &hash, &role, &specialty, &mustChange)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if mustChange {
		tempToken, err := makeTempToken(userID, req.Email, role)
		if err != nil {
			internalError(c, "Auth.Login/temptoken", err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status": "password_change_required",
			"token":  tempToken,
			"user": gin.H{
				"id":        userID,
				"email":     req.Email,
				"role":      role,
				"specialty": specialty,
			},
		})
		return
	}

	token, err := makeToken(userID, req.Email, role)
	if err != nil {
		internalError(c, "Auth.Login/token", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": token, "user_id": userID, "role": role, "specialty": specialty})
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := context.Background()

	var email, role string
	err := h.db.QueryRow(ctx,
		`SELECT u.email, COALESCE(p.role, 'resident')
		 FROM users u LEFT JOIN profiles p ON p.id = u.id
		 WHERE u.id = $1`, userID,
	).Scan(&email, &role)
	if err != nil {
		internalError(c, "Auth.Refresh/query", err)
		return
	}

	token, err := makeToken(userID, email, role)
	if err != nil {
		internalError(c, "Auth.Refresh/token", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": token, "user_id": userID, "role": role})
}

func (h *AuthHandler) Me(c *gin.Context) {
	userID := c.GetString("user_id")
	ctx := context.Background()

	var p profileRow
	row := h.db.QueryRow(ctx,
		`SELECT`+profileCols+` FROM profiles WHERE id = $1`, userID)
	if err := p.scanFrom(row); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "profile not found"})
		return
	}
	c.JSON(http.StatusOK, p)
}

// POST /auth/change-password-first-login
// Public endpoint that accepts ONLY a temporary JWT (the one Login returns when
// password_change_required is set). It hashes the new password, clears the flag,
// and returns a normal long-lived JWT.
type changePasswordFirstLoginReq struct {
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

func (h *AuthHandler) ChangePasswordFirstLogin(c *gin.Context) {
	var req changePasswordFirstLoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims, err := parseBearerToken(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if !claims.Temp {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "temporary token required"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		internalError(c, "Auth.ChangePasswordFirstLogin/bcrypt", err)
		return
	}

	ctx := context.Background()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		internalError(c, "Auth.ChangePasswordFirstLogin/begin", err)
		return
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		string(hash), claims.UserID,
	); err != nil {
		internalError(c, "Auth.ChangePasswordFirstLogin/users", err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE profiles SET password_change_required = FALSE, updated_at = NOW() WHERE id = $1`,
		claims.UserID,
	); err != nil {
		internalError(c, "Auth.ChangePasswordFirstLogin/profiles", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		internalError(c, "Auth.ChangePasswordFirstLogin/commit", err)
		return
	}

	token, err := makeToken(claims.UserID, claims.Email, claims.Role)
	if err != nil {
		internalError(c, "Auth.ChangePasswordFirstLogin/token", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"token":   token,
		"user_id": claims.UserID,
		"role":    claims.Role,
	})
}

// POST /auth/change-password — авторизованная смена пароля из профиля.
type changePasswordReq struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password"     binding:"required,min=6"`
}

func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req changePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.CurrentPassword == req.NewPassword {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new password must differ from current"})
		return
	}

	userID := c.GetString("user_id")
	ctx := context.Background()

	var currentHash string
	if err := h.db.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID,
	).Scan(&currentHash); err != nil {
		internalError(c, "Auth.ChangePassword/query", err)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(req.CurrentPassword)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		internalError(c, "Auth.ChangePassword/bcrypt", err)
		return
	}

	if _, err := h.db.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		string(newHash), userID,
	); err != nil {
		internalError(c, "Auth.ChangePassword/update", err)
		return
	}

	email := c.GetString("user_email")
	role := c.GetString("user_role")
	token, err := makeToken(userID, email, role)
	if err != nil {
		internalError(c, "Auth.ChangePassword/token", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token})
}

// POST /auth/forgot-password — generates a 6-digit code, stores it for 15 minutes,
// and delivers it via FCM push to all of the user's registered devices.
// Always returns 200 to avoid leaking which emails are registered.
type forgotPasswordReq struct {
	Email string `json:"email" binding:"required,email"`
}

func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req forgotPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()
	email := strings.ToLower(strings.TrimSpace(req.Email))

	var userID string
	err := h.db.QueryRow(ctx, `SELECT id FROM users WHERE email = $1`, email).Scan(&userID)
	if err != nil {
		// Don't leak whether the email exists.
		c.JSON(http.StatusOK, gin.H{"message": "if the email is registered, a code was sent"})
		return
	}

	code, err := generateResetCode()
	if err != nil {
		internalError(c, "Auth.ForgotPassword/code", err)
		return
	}

	expiresAt := time.Now().Add(15 * time.Minute)
	if _, err := h.db.Exec(ctx, `
		INSERT INTO password_reset_codes (user_id, code, expires_at)
		VALUES ($1, $2, $3)`,
		userID, code, expiresAt,
	); err != nil {
		internalError(c, "Auth.ForgotPassword/insert", err)
		return
	}

	if h.mail != nil {
		if err := h.mail.SendCode(email, code); err != nil {
			// Log the error but still return 200 — we already saved the code, the
			// user can retry, and we don't want to leak whether SMTP failed vs.
			// whether the email was unknown.
			log.Printf("[mail] send reset code to %s: %v", email, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "if the email is registered, a code was sent"})
}

// POST /auth/reset-password — verifies code, hashes new password, marks code used,
// returns a fresh JWT.
type resetPasswordReq struct {
	Email       string `json:"email"        binding:"required,email"`
	Code        string `json:"code"         binding:"required,len=6"`
	NewPassword string `json:"new_password" binding:"required,min=6"`
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req resetPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()
	email := strings.ToLower(strings.TrimSpace(req.Email))

	var (
		userID, role string
		codeID       string
	)
	err := h.db.QueryRow(ctx, `
		SELECT prc.id, u.id, COALESCE(p.role, 'resident')
		FROM password_reset_codes prc
		JOIN users u ON u.id = prc.user_id
		LEFT JOIN profiles p ON p.id = u.id
		WHERE u.email = $1
		  AND prc.code = $2
		  AND prc.used = FALSE
		  AND prc.expires_at > NOW()
		ORDER BY prc.created_at DESC
		LIMIT 1`,
		email, req.Code,
	).Scan(&codeID, &userID, &role)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired code"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		internalError(c, "Auth.ResetPassword/bcrypt", err)
		return
	}

	tx, err := h.db.Begin(ctx)
	if err != nil {
		internalError(c, "Auth.ResetPassword/begin", err)
		return
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = NOW() WHERE id = $2`,
		string(hash), userID,
	); err != nil {
		internalError(c, "Auth.ResetPassword/users", err)
		return
	}
	if _, err := tx.Exec(ctx,
		`UPDATE password_reset_codes SET used = TRUE WHERE id = $1`, codeID,
	); err != nil {
		internalError(c, "Auth.ResetPassword/markused", err)
		return
	}
	// If a staff member used forgot-password before completing the forced change,
	// treat that as fulfilling the requirement.
	if _, err := tx.Exec(ctx,
		`UPDATE profiles SET password_change_required = FALSE, updated_at = NOW() WHERE id = $1`,
		userID,
	); err != nil {
		internalError(c, "Auth.ResetPassword/profiles", err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		internalError(c, "Auth.ResetPassword/commit", err)
		return
	}

	token, err := makeToken(userID, email, role)
	if err != nil {
		internalError(c, "Auth.ResetPassword/token", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token, "user_id": userID, "role": role})
}

func makeToken(userID, email, role string) (string, error) {
	claims := middleware.Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString([]byte(os.Getenv("JWT_SECRET")))
}

// makeTempToken issues a 15-minute token marked Temp=true. The auth middleware
// rejects it everywhere except ChangePasswordFirstLogin, which parses it directly.
func makeTempToken(userID, email, role string) (string, error) {
	claims := middleware.Claims{
		UserID: userID,
		Email:  email,
		Role:   role,
		Temp:   true,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString([]byte(os.Getenv("JWT_SECRET")))
}

func parseBearerToken(c *gin.Context) (*middleware.Claims, error) {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, fmt.Errorf("missing token")
	}
	raw := strings.TrimPrefix(h, "Bearer ")
	tok, err := jwt.ParseWithClaims(raw, &middleware.Claims{},
		func(t *jwt.Token) (any, error) { return []byte(os.Getenv("JWT_SECRET")), nil },
	)
	if err != nil || !tok.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return tok.Claims.(*middleware.Claims), nil
}

func generateResetCode() (string, error) {
	const digits = "0123456789"
	out := make([]byte, 6)
	max := big.NewInt(int64(len(digits)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = digits[n.Int64()]
	}
	return string(out), nil
}
