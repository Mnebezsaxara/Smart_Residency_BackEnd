package handler

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NewsHandler struct{ db *pgxpool.Pool }

func NewNewsHandler(db *pgxpool.Pool) *NewsHandler { return &NewsHandler{db: db} }

type newsItem struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Author    string    `json:"author"`
	Entrance  *int      `json:"entrance"`
	IsPinned  bool      `json:"is_pinned"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GET /news — for residents, returns общие новости (entrance IS NULL)
// плюс новости, адресованные их подъезду. Staff/admin/security видят всё.
//
// We pass two args to a single SQL statement: `entrance` and `restrict`.
// When restrict=FALSE (non-resident) the WHERE clause is satisfied for every row;
// when restrict=TRUE we filter to "общие или мой подъезд".
func (h *NewsHandler) List(c *gin.Context) {
	userID := c.GetString("user_id")
	role := c.GetString("user_role")
	ctx := context.Background()

	var entrance *int
	restrict := false
	if role == "resident" {
		restrict = true
		if err := h.db.QueryRow(ctx,
			`SELECT entrance FROM profiles WHERE id = $1`, userID,
		).Scan(&entrance); err != nil {
			internalError(c, "News.List/entrance", err)
			return
		}
	}

	rows, err := h.db.Query(ctx, `
		SELECT n.id, n.title, n.body, COALESCE(p.full_name, '') AS author,
		       n.entrance, n.is_pinned, n.created_at, n.updated_at
		FROM news n
		LEFT JOIN profiles p ON p.id = n.author_id
		WHERE NOT $1
		   OR n.entrance IS NULL
		   OR n.entrance = $2
		ORDER BY n.is_pinned DESC, n.created_at DESC`, restrict, entrance)
	if err != nil {
		internalError(c, "News.List/query", err)
		return
	}
	defer rows.Close()

	out := []newsItem{}
	for rows.Next() {
		var n newsItem
		if err := rows.Scan(
			&n.ID, &n.Title, &n.Body, &n.Author,
			&n.Entrance, &n.IsPinned, &n.CreatedAt, &n.UpdatedAt,
		); err != nil {
			internalError(c, "News.List/scan", err)
			return
		}
		out = append(out, n)
	}
	c.JSON(http.StatusOK, out)
}

type newsCreateReq struct {
	Title    string `json:"title"     binding:"required"`
	Body     string `json:"body"      binding:"required"`
	Entrance *int   `json:"entrance"`
	IsPinned bool   `json:"is_pinned"`
}

// POST /admin/news
func (h *NewsHandler) Create(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	var req newsCreateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(req.Body)
	if title == "" || body == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title and body must not be empty"})
		return
	}

	authorID := c.GetString("user_id")
	id := uuid.New().String()
	ctx := context.Background()

	var created newsItem
	created.ID = id
	if err := h.db.QueryRow(ctx, `
		INSERT INTO news (id, title, body, author_id, entrance, is_pinned)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING entrance, is_pinned, created_at, updated_at`,
		id, title, body, authorID, req.Entrance, req.IsPinned,
	).Scan(&created.Entrance, &created.IsPinned, &created.CreatedAt, &created.UpdatedAt); err != nil {
		internalError(c, "News.Create/insert", err)
		return
	}
	created.Title = title
	created.Body = body
	_ = h.db.QueryRow(ctx,
		`SELECT COALESCE(full_name, '') FROM profiles WHERE id = $1`, authorID,
	).Scan(&created.Author)

	c.JSON(http.StatusCreated, created)
}

type newsUpdateReq struct {
	Title    *string `json:"title"`
	Body     *string `json:"body"`
	Entrance *int    `json:"entrance"`
	IsPinned *bool   `json:"is_pinned"`
	ClearEntrance bool `json:"clear_entrance"` // explicit flag: set entrance to NULL
}

// PUT /admin/news/:id
func (h *NewsHandler) Update(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	id := c.Param("id")
	var req newsUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sets := []string{}
	args := []any{}
	i := 1
	add := func(col string, val any) {
		sets = append(sets, col+" = $"+itoa(i))
		args = append(args, val)
		i++
	}
	if req.Title != nil {
		add("title", strings.TrimSpace(*req.Title))
	}
	if req.Body != nil {
		add("body", strings.TrimSpace(*req.Body))
	}
	if req.ClearEntrance {
		sets = append(sets, "entrance = NULL")
	} else if req.Entrance != nil {
		add("entrance", *req.Entrance)
	}
	if req.IsPinned != nil {
		add("is_pinned", *req.IsPinned)
	}
	if len(sets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
		return
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)

	q := "UPDATE news SET " + strings.Join(sets, ", ") + " WHERE id = $" + itoa(i)
	ctx := context.Background()
	tag, err := h.db.Exec(ctx, q, args...)
	if err != nil {
		internalError(c, "News.Update/exec", err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "news not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DELETE /admin/news/:id
func (h *NewsHandler) Delete(c *gin.Context) {
	if c.GetString("user_role") != "admin" {
		forbiddenAccess(c, "admin only")
		return
	}
	id := c.Param("id")
	tag, err := h.db.Exec(context.Background(), `DELETE FROM news WHERE id = $1`, id)
	if err != nil {
		internalError(c, "News.Delete/exec", err)
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "news not found"})
		return
	}
	c.Status(http.StatusNoContent)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
