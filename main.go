package main

import (
	"database/sql"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
)

// -----------------------------
// Структуры данных
// -----------------------------

type Post struct {
	PostID     int
	ThreadID   int
	UserID     int
	Username   string
	Content    string
	CreatedAt  time.Time
	ImageIDs   []int
	Comments   []Comment
}

type Thread struct {
	ThreadID  int
	Title     string
	CreatedAt time.Time
}

type Comment struct {
	CommentID int
	PostID    int
	UserID    int
	Username  string
	Content   string
	CreatedAt time.Time
	ImageIDs  []int
}

// Данные для шаблона main.html
type TemplateData struct {
	Posts         []Post
	Threads       []Thread
	CurrentThread *Thread
}

// -----------------------------
// Подключение к БД
// -----------------------------

func connectToDB() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_PUBLIC_URL")
	if dsn == "" {
		// Настройте под свои параметры, если требуется
		dsn = "host=switchyard.proxy.rlwy.net port=48837 user=postgres password=jtYqvohthKjvrJMpGnvivJWcLcwUSzm dbname=railway sslmode=disable"
	}

	return sql.Open("postgres", dsn)
}

// -----------------------------
// Треды и посты
// -----------------------------

func createThread(db *sql.DB, title string) (int, error) {
	const query = `
		INSERT INTO threads (title, created_at)
		VALUES ($1, NOW())
		RETURNING thread_id;
	`
	var newID int
	err := db.QueryRow(query, title).Scan(&newID)
	if err != nil {
		return 0, fmt.Errorf("createThread: %w", err)
	}
	return newID, nil
}

func getAllThreads(db *sql.DB) ([]Thread, error) {
	const query = `
		SELECT thread_id, title, created_at
		FROM threads
		ORDER BY created_at DESC;
	`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("getAllThreads: %w", err)
	}
	defer rows.Close()

	var threads []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ThreadID, &t.Title, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("getAllThreads scan: %w", err)
		}
		threads = append(threads, t)
	}
	return threads, nil
}

func getThreadByID(db *sql.DB, id int) (Thread, error) {
	const query = `
		SELECT thread_id, title, created_at
		FROM threads
		WHERE thread_id = $1;
	`
	var t Thread
	err := db.QueryRow(query, id).Scan(&t.ThreadID, &t.Title, &t.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return t, fmt.Errorf("thread с ID=%d не найден", id)
		}
		return t, fmt.Errorf("getThreadByID: %w", err)
	}
	return t, nil
}

func createPost(db *sql.DB, threadID, userID int, content string) (int, error) {
	const query = `
		INSERT INTO posts (thread_id, user_id, content, created_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING post_id;
	`
	var newID int
	err := db.QueryRow(query, threadID, userID, content).Scan(&newID)
	if err != nil {
		return 0, fmt.Errorf("createPost: %w", err)
	}
	return newID, nil
}

func getRecentPosts(db *sql.DB) ([]Post, error) {
	const query = `
		SELECT
			p.post_id,
			p.thread_id,
			p.user_id,
			u.username,
			p.content,
			p.created_at
		FROM posts p
		JOIN users u ON p.user_id = u.user_id
		ORDER BY p.created_at DESC
		LIMIT 20;
	`
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("getRecentPosts: %w", err)
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.PostID, &p.ThreadID, &p.UserID, &p.Username, &p.Content, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("getRecentPosts scan: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func getPostsByThreadID(db *sql.DB, threadID int) ([]Post, error) {
	const query = `
		SELECT
			p.post_id,
			p.thread_id,
			p.user_id,
			u.username,
			p.content,
			p.created_at
		FROM posts p
		JOIN users u ON p.user_id = u.user_id
		WHERE p.thread_id = $1
		ORDER BY p.created_at ASC;
	`
	rows, err := db.Query(query, threadID)
	if err != nil {
		return nil, fmt.Errorf("getPostsByThreadID: %w", err)
	}
	defer rows.Close()

	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.PostID, &p.ThreadID, &p.UserID, &p.Username, &p.Content, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("getPostsByThreadID scan: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, nil
}

// -----------------------------
// Изображения в постах
// -----------------------------

func getImageIDsByPostID(db *sql.DB, postID int) ([]int, error) {
	const query = `
		SELECT image_id
		FROM post_images
		WHERE post_id = $1
		ORDER BY image_id ASC;
	`
	rows, err := db.Query(query, postID)
	if err != nil {
		return nil, fmt.Errorf("getImageIDsByPostID: %w", err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var imgID int
		if err := rows.Scan(&imgID); err != nil {
			return nil, fmt.Errorf("getImageIDsByPostID scan: %w", err)
		}
		ids = append(ids, imgID)
	}
	return ids, nil
}

func savePostImage(db *sql.DB, postID int, file multipart.File, header *multipart.FileHeader) error {
	imgBytes, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("savePostImage: %w", err)
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		ext := filepath.Ext(header.Filename)
		mimeType = mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}
	const query = `
		INSERT INTO post_images (post_id, image_data, image_mime)
		VALUES ($1, $2, $3);
	`
	if _, err := db.Exec(query, postID, imgBytes, mimeType); err != nil {
		return fmt.Errorf("savePostImage: %w", err)
	}
	return nil
}

// -----------------------------
// Комментарии
// -----------------------------

func getCommentsByPostID(db *sql.DB, postID int) ([]Comment, error) {
	const query = `
		SELECT
			c.comment_id,
			c.post_id,
			c.user_id,
			u.username,
			c.content,
			c.created_at
		FROM comments c
		JOIN users u ON c.user_id = u.user_id
		WHERE c.post_id = $1
		ORDER BY c.created_at ASC;
	`
	rows, err := db.Query(query, postID)
	if err != nil {
		return nil, fmt.Errorf("getCommentsByPostID: %w", err)
	}
	defer rows.Close()

	var comments []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.CommentID, &c.PostID, &c.UserID, &c.Username, &c.Content, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("getCommentsByPostID scan: %w", err)
		}
		comments = append(comments, c)
	}
	return comments, nil
}

// Для изображений в комментариях:
func getImageIDsByCommentID(db *sql.DB, commentID int) ([]int, error) {
	const query = `
		SELECT image_id
		FROM comment_images
		WHERE comment_id = $1
		ORDER BY image_id ASC;
	`
	rows, err := db.Query(query, commentID)
	if err != nil {
		return nil, fmt.Errorf("getImageIDsByCommentID: %w", err)
	}
	defer rows.Close()

	var ids []int
	for rows.Next() {
		var imgID int
		if err := rows.Scan(&imgID); err != nil {
			return nil, fmt.Errorf("getImageIDsByCommentID scan: %w", err)
		}
		ids = append(ids, imgID)
	}
	return ids, nil
}

func saveCommentImage(db *sql.DB, commentID int, file multipart.File, header *multipart.FileHeader) error {
	imgBytes, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("saveCommentImage: %w", err)
	}
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		ext := filepath.Ext(header.Filename)
		mimeType = mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}
	const query = `
		INSERT INTO comment_images (comment_id, image_data, image_mime)
		VALUES ($1, $2, $3);
	`
	if _, err := db.Exec(query, commentID, imgBytes, mimeType); err != nil {
		return fmt.Errorf("saveCommentImage: %w", err)
	}
	return nil
}

// -----------------------------
// Обработчики HTTP
// -----------------------------

// 1) Создание треда (POST /threads)
func addThreadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Не удалось прочитать форму", http.StatusBadRequest)
		return
	}
	title := r.FormValue("title")
	if title == "" {
		http.Error(w, "Название треда не может быть пустым", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	newThreadID, err := createThread(db, title)
	if err != nil {
		http.Error(w, "Не удалось создать тред: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("Создан новый тред, ID = %d", newThreadID)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// 2) Создание поста (POST /posts)
func addPostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Не удалось разобрать форму", http.StatusBadRequest)
		return
	}

	threadID, err1 := strconv.Atoi(r.FormValue("thread_id"))
	userID, err2 := strconv.Atoi(r.FormValue("user_id"))
	content := r.FormValue("content")
	if err1 != nil || err2 != nil || content == "" {
		http.Error(w, "Неверные поля формы для поста", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	newPostID, err := createPost(db, threadID, userID, content)
	if err != nil {
		http.Error(w, "Не удалось сохранить пост: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("Создан новый пост (thread_id=%d) ID = %d", threadID, newPostID)

	// Загружаем фото к посту (если есть)
	file, header, err := r.FormFile("image")
	if err == nil && header.Filename != "" {
		defer file.Close()
		if saveErr := savePostImage(db, newPostID, file, header); saveErr != nil {
			log.Printf("Ошибка сохранения картинки для post_id=%d: %v", newPostID, saveErr)
		}
	}

	// После создания редиректим сразу на страницу этого треда
	http.Redirect(w, r, fmt.Sprintf("/?thread_id=%d", threadID), http.StatusSeeOther)
}

// 3) Создание комментария (POST /comments)
func addCommentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(5 << 20); err != nil {
		http.Error(w, "Не удалось разобрать форму", http.StatusBadRequest)
		return
	}

	threadID, err1 := strconv.Atoi(r.FormValue("thread_id"))
	postID, err2 := strconv.Atoi(r.FormValue("post_id"))
	userID, err3 := strconv.Atoi(r.FormValue("user_id"))
	content := r.FormValue("content")
	if err1 != nil || err2 != nil || err3 != nil || content == "" {
		http.Error(w, "Неверные поля формы для комментария", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Вставляем сам комментарий
	const insertComment = `
		INSERT INTO comments (post_id, user_id, content, created_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING comment_id;
	`
	var newCommentID int
	if err := db.QueryRow(insertComment, postID, userID, content).Scan(&newCommentID); err != nil {
		http.Error(w, "Не удалось сохранить комментарий: "+err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("Создан новый комментарий (post_id=%d) ID = %d", postID, newCommentID)

	// Загружаем фото к комментарию (если есть)
	file, header, err := r.FormFile("image")
	if err == nil && header.Filename != "" {
		defer file.Close()
		if saveErr := saveCommentImage(db, newCommentID, file, header); saveErr != nil {
			log.Printf("Ошибка сохранения картинки для comment_id=%d: %v", newCommentID, saveErr)
		}
	}

	// Проверяем, был ли запрос с redirect_thread=1
	if r.FormValue("redirect_thread") == "1" {
		http.Redirect(w, r, fmt.Sprintf("/?thread_id=%d", threadID), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// 4) Основная страница (GET / или /?thread_id=…)
func mainFunc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	var (
		posts         []Post
		currentThread *Thread
	)

	threadParam := r.URL.Query().Get("thread_id")
	if threadParam != "" {
		tid, err := strconv.Atoi(threadParam)
		if err != nil {
			http.Error(w, "Неверный thread_id", http.StatusBadRequest)
			return
		}
		th, err := getThreadByID(db, tid)
		if err != nil {
			http.Error(w, "Тред не найден: "+err.Error(), http.StatusNotFound)
			return
		}
		currentThread = &th

		posts, err = getPostsByThreadID(db, tid)
		if err != nil {
			http.Error(w, "Не удалось получить посты треда: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		posts, err = getRecentPosts(db)
		if err != nil {
			http.Error(w, "Не удалось получить последние посты: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Для каждого поста достаём картинки и комментарии (+ картинки комментариев)
	for i := range posts {
		ids, err := getImageIDsByPostID(db, posts[i].PostID)
		if err != nil {
			http.Error(w, "Ошибка загрузки картинок поста: "+err.Error(), http.StatusInternalServerError)
			return
		}
		posts[i].ImageIDs = ids

		comments, err := getCommentsByPostID(db, posts[i].PostID)
		if err != nil {
			http.Error(w, "Ошибка загрузки комментариев: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Для каждого комментария достаём картинки
		for j := range comments {
			cids, err := getImageIDsByCommentID(db, comments[j].CommentID)
			if err != nil {
				http.Error(w, "Ошибка загрузки картинок комментария: "+err.Error(), http.StatusInternalServerError)
				return
			}
			comments[j].ImageIDs = cids
		}

		posts[i].Comments = comments
	}

	threads, err := getAllThreads(db)
	if err != nil {
		http.Error(w, "Не удалось получить треды: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := TemplateData{
		Posts:         posts,
		Threads:       threads,
		CurrentThread: currentThread,
	}

	tmpl := template.Must(template.ParseFiles("html/main.html"))
	if err := tmpl.ExecuteTemplate(w, "main", data); err != nil {
		http.Error(w, "Ошибка рендеринга шаблона: "+err.Error(), http.StatusInternalServerError)
	}
}

// 5) Отдача картинок по image_id (для post_images и comment_images)
func serveImageHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	imgID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Неверный image_id", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Сначала пробуем найти картинку в post_images:
	const queryPostImg = `
		SELECT image_data, image_mime
		FROM post_images
		WHERE image_id = $1;
	`
	var blob []byte
	var mimeType string
	err = db.QueryRow(queryPostImg, imgID).Scan(&blob, &mimeType)
	if err == sql.ErrNoRows {
		// Если нет в post_images, попробуем в comment_images:
		const queryCommentImg = `
			SELECT image_data, image_mime
			FROM comment_images
			WHERE image_id = $1;
		`
		err = db.QueryRow(queryCommentImg, imgID).Scan(&blob, &mimeType)
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, "Ошибка при чтении картинки: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else if err != nil {
		http.Error(w, "Ошибка при чтении картинки: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(blob)))
	w.Write(blob)
}

// -----------------------------
// main()
// -----------------------------

func main() {
	rtr := mux.NewRouter()

	// Статика: CSS
	rtr.PathPrefix("/css/").
		Handler(http.StripPrefix("/css/", http.FileServer(http.Dir("./css/"))))

	// Маршруты
	rtr.HandleFunc("/", mainFunc).Methods("GET")
	rtr.HandleFunc("/threads", addThreadHandler).Methods("POST")
	rtr.HandleFunc("/posts", addPostHandler).Methods("POST")
	rtr.HandleFunc("/comments", addCommentHandler).Methods("POST")
	rtr.HandleFunc("/images/{id}", serveImageHandler).Methods("GET")

	fmt.Println("Сервер запущен на :8080")
	log.Fatal(http.ListenAndServe(":8080", rtr))
}
