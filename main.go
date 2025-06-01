package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	PostID    int
	ThreadID  int
	UserID    int
	Username  string
	Content   string
	CreatedAt time.Time
	ImageIDs  []int
	Comments  []Comment
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
}

// Данные для шаблона main.html
type TemplateData struct {
	Posts         []Post
	Threads       []Thread
	CurrentThread *Thread
}

// Данные для шаблона thread.html
type ThreadPageData struct {
	Thread  Thread
	Posts   []Post
	Threads []Thread
}

// Данные для шаблона comment.html
type CommentFormData struct {
	ThreadID int
	PostID   int
	Threads  []Thread
}

// -----------------------------
// Подключение к БД
// -----------------------------

func connectToDB() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_PUBLIC_URL")
	if dsn == "" {
		dsn = "host=switchyard.proxy.rlwy.net port=48837 user=postgres password=jtYqvohthKjvrJMpGnvivJWcLcwUSzmD@switchyard dbname=railway sslmode=disable"
	}
	
	return sql.Open("postgres", dsn)
}

// -----------------------------
// Пользователи (регистрация)
// -----------------------------

func createUser(db *sql.DB, username, passwordHash string) (int, error) {
	const query = `
		INSERT INTO users (username, password_hash)
		VALUES ($1, $2)
		RETURNING user_id;
	`
	var newID int
	err := db.QueryRow(query, username, passwordHash).Scan(&newID)
	if err != nil {
		return 0, fmt.Errorf("createUser: %w", err)
	}
	return newID, nil
}

func newUserFormHandler(w http.ResponseWriter, r *http.Request) {
	// Простейшая страница регистрации (можно убрать, если не нужен)
	tmpl := template.Must(template.ParseFiles("html/user.html"))
	_ = tmpl.ExecuteTemplate(w, "new_user", nil)
}

func addUserHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Не удалось прочитать форму", http.StatusBadRequest)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	if username == "" || password == "" || confirm == "" {
		http.Error(w, "Все поля обязательны", http.StatusBadRequest)
		return
	}
	if password != confirm {
		http.Error(w, "Пароли не совпадают", http.StatusBadRequest)
		return
	}

	hash := sha256.Sum256([]byte(password))
	passwordHash := hex.EncodeToString(hash[:])

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	_, err = createUser(db, username, passwordHash)
	if err != nil {

		http.Error(w, "Не удалось создать пользователя", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
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
	bytes, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("savePostImage read: %w", err)
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
	if _, err := db.Exec(query, postID, bytes, mimeType); err != nil {
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

func addCommentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Не удалось прочитать форму", http.StatusBadRequest)
		return
	}

	// Парсим thread_id и post_id из формы
	threadID, err1 := strconv.Atoi(r.FormValue("thread_id"))
	postID, err2 := strconv.Atoi(r.FormValue("post_id"))
	userID, err3 := strconv.Atoi(r.FormValue("user_id"))
	content := r.FormValue("content")
	if err1 != nil || err2 != nil || err3 != nil || content == "" {
		http.Error(w, "Неверные поля формы", http.StatusBadRequest)
		return
	}

	// Сохраняем комментарий
	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	const query = `
		INSERT INTO comments (post_id, user_id, content, created_at)
		VALUES ($1, $2, $3, NOW());
	`
	if _, err := db.Exec(query, postID, userID, content); err != nil {
		http.Error(w, "Не удалось сохранить комментарий", http.StatusInternalServerError)
		return
	}

	// Если был выбран конкретный тред, возвращаем пользователя обратно на main с ?thread_id=…
	if threadIDStr := r.FormValue("redirect_thread"); threadIDStr != "" {
		http.Redirect(w, r, fmt.Sprintf("/?thread_id=%d", threadID), http.StatusSeeOther)
		return
	}
	// Иначе просто на главную
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func newCommentFormHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	threadIDStr := q.Get("thread_id")
	postIDStr := q.Get("post_id")
	if threadIDStr == "" || postIDStr == "" {
		http.Error(w, "thread_id и post_id обязательны", http.StatusBadRequest)
		return
	}
	threadID, err1 := strconv.Atoi(threadIDStr)
	postID, err2 := strconv.Atoi(postIDStr)
	if err1 != nil || err2 != nil {
		http.Error(w, "Неверные параметры", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	threads, err := getAllThreads(db)
	if err != nil {
		http.Error(w, "Не удалось получить треды", http.StatusInternalServerError)
		return
	}

	data := CommentFormData{
		ThreadID: threadID,
		PostID:   postID,
		Threads:  threads,
	}
	tmpl := template.Must(template.ParseFiles("html/comment.html"))
	_ = tmpl.ExecuteTemplate(w, "comment_form", data)
}

// -----------------------------
// Обработчик создания поста
// -----------------------------

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
		http.Error(w, "Неверные поля формы", http.StatusBadRequest)
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
		http.Error(w, "Не удалось сохранить пост", http.StatusInternalServerError)
		return
	}

	file, header, err := r.FormFile("image")
	if err == nil && header.Filename != "" {
		defer file.Close()
		if saveErr := savePostImage(db, newPostID, file, header); saveErr != nil {
			log.Printf("Ошибка при сохранении картинки: %v", saveErr)
		}
	}

	// Редиректим на страницу треда, а не на главную
	http.Redirect(w, r, fmt.Sprintf("/thread/%d", threadID), http.StatusSeeOther)
}

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
		http.Error(w, "Название треда пустое", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	_, err = createThread(db, title)
	if err != nil {
		http.Error(w, "Не удалось создать тред", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// -----------------------------
// Главная страница и страница треда
// -----------------------------

func mainFunc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	var posts []Post
	var currentThread *Thread

	threadParam := r.URL.Query().Get("thread_id")
	if threadParam != "" {
		tid, err := strconv.Atoi(threadParam)
		if err != nil {
			http.Error(w, "Неверный thread_id", http.StatusBadRequest)
			return
		}
		th, err := getThreadByID(db, tid)
		if err != nil {
			http.Error(w, "Тред не найден", http.StatusNotFound)
			return
		}
		currentThread = &th

		posts, err = getPostsByThreadID(db, tid)
		if err != nil {
			http.Error(w, "Не удалось получить посты", http.StatusInternalServerError)
			return
		}
	} else {
		posts, err = getRecentPosts(db)
		if err != nil {
			http.Error(w, "Не удалось получить последние посты", http.StatusInternalServerError)
			return
		}
	}

	// Для каждого поста достаём картинки и комментарии
	for i := range posts {
		ids, _ := getImageIDsByPostID(db, posts[i].PostID)
		posts[i].ImageIDs = ids
		comments, _ := getCommentsByPostID(db, posts[i].PostID)
		posts[i].Comments = comments
	}

	threads, err := getAllThreads(db)
	if err != nil {
		http.Error(w, "Не удалось получить треды", http.StatusInternalServerError)
		return
	}

	data := TemplateData{
		Posts:         posts,
		Threads:       threads,
		CurrentThread: currentThread,
	}

	tmpl := template.Must(template.ParseFiles("html/main.html"))
	_ = tmpl.ExecuteTemplate(w, "main", data)
}

func threadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	vars := mux.Vars(r)
	idStr := vars["id"]
	threadID, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Неверный ID треда", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка подключения к БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	thread, err := getThreadByID(db, threadID)
	if err != nil {
		http.Error(w, "Тред не найден", http.StatusNotFound)
		return
	}

	posts, err := getPostsByThreadID(db, threadID)
	if err != nil {
		http.Error(w, "Не удалось получить посты", http.StatusInternalServerError)
		return
	}

	for i := range posts {
		ids, _ := getImageIDsByPostID(db, posts[i].PostID)
		posts[i].ImageIDs = ids
		comments, _ := getCommentsByPostID(db, posts[i].PostID)
		posts[i].Comments = comments
	}

	threads, err := getAllThreads(db)
	if err != nil {
		http.Error(w, "Не удалось получить треды", http.StatusInternalServerError)
		return
	}

	data := ThreadPageData{
		Thread:  thread,
		Posts:   posts,
		Threads: threads,
	}

	tmpl := template.Must(template.ParseFiles("html/thread.html"))
	_ = tmpl.ExecuteTemplate(w, "thread", data)
}

// -----------------------------
// Картинки
// -----------------------------

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

	const query = `
		SELECT image_data, image_mime
		FROM post_images
		WHERE image_id = $1;
	`
	var blob []byte
	var mimeType string
	err = db.QueryRow(query, imgID).Scan(&blob, &mimeType)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Ошибка при чтении изображения", http.StatusInternalServerError)
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

	// Сервим CSS
	rtr.PathPrefix("/css/").Handler(http.StripPrefix("/css/", http.FileServer(http.Dir("./css/"))))

	// Маршруты
	rtr.HandleFunc("/", mainFunc).Methods("GET")
	rtr.HandleFunc("/users/new", newUserFormHandler).Methods("GET")
	rtr.HandleFunc("/users", addUserHandler).Methods("POST")
	rtr.HandleFunc("/threads", addThreadHandler).Methods("POST")
	rtr.HandleFunc("/posts", addPostHandler).Methods("POST")
	rtr.HandleFunc("/thread/{id}", threadHandler).Methods("GET")
	rtr.HandleFunc("/comments/new", newCommentFormHandler).Methods("GET")
	rtr.HandleFunc("/comments", addCommentHandler).Methods("POST")
	rtr.HandleFunc("/images/{id}", serveImageHandler).Methods("GET")

	fmt.Println("Сервер запущен на :8080")
	log.Fatal(http.ListenAndServe(":8080", rtr))
}
