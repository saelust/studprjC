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

// --- структуры данных ---

type Post struct {
	PostID    int
	ThreadID  int
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
	Username  string
	Content   string
	CreatedAt time.Time
	ImageIDs  []int
}

type TemplateData struct {
	Posts         []Post
	Threads       []Thread
	CurrentThread *Thread
}

// --- подключение к БД ---

func connectToDB() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_PUBLIC_URL")
	if dsn == "" {
		dsn = "host=switchyard.proxy.rlwy.net port=48837 user=postgres password=jtYqvohthKjvrJMpGnvivJWcLcwUSzmD dbname=railway sslmode=disable"
	}
	return sql.Open("postgres", dsn)
}

// --- треды и случайный тред ---

func createThread(db *sql.DB, title string) (int, error) {
	const q = `INSERT INTO threads (title, created_at) VALUES ($1, NOW()) RETURNING thread_id`
	var id int
	err := db.QueryRow(q, title).Scan(&id)
	return id, err
}
func getAllThreads(db *sql.DB) ([]Thread, error) {
	const q = `SELECT thread_id, title, created_at FROM threads ORDER BY created_at DESC`
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ts []Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(&t.ThreadID, &t.Title, &t.CreatedAt); err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}
	return ts, nil
}
func getThreadByID(db *sql.DB, id int) (Thread, error) {
	const q = `SELECT thread_id, title, created_at FROM threads WHERE thread_id=$1`
	var t Thread
	err := db.QueryRow(q, id).Scan(&t.ThreadID, &t.Title, &t.CreatedAt)
	return t, err
}
func getRandomThread(db *sql.DB) (Thread, error) {
	const q = `SELECT thread_id, title, created_at FROM get_random_thread()`
	var t Thread
	err := db.QueryRow(q).Scan(&t.ThreadID, &t.Title, &t.CreatedAt)
	return t, err
}

// --- посты ---

func createPost(db *sql.DB, threadID int, username, content string) (int, error) {
	const q = `INSERT INTO posts (thread_id, username, content, created_at) VALUES ($1,$2,$3,NOW()) RETURNING post_id`
	var id int
	err := db.QueryRow(q, threadID, username, content).Scan(&id)
	return id, err
}
func getRecentPosts(db *sql.DB) ([]Post, error) {
	const q = `SELECT post_id, thread_id, username, content, created_at FROM posts ORDER BY created_at DESC LIMIT 20`
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ps []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.PostID, &p.ThreadID, &p.Username, &p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, nil
}
func getPostsByThreadID(db *sql.DB, threadID int) ([]Post, error) {
	const q = `SELECT post_id, thread_id, username, content, created_at FROM posts WHERE thread_id=$1 ORDER BY created_at`
	rows, err := db.Query(q, threadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ps []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.PostID, &p.ThreadID, &p.Username, &p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		ps = append(ps, p)
	}
	return ps, nil
}
func updatePostContent(db *sql.DB, postID int, content string) error {
	_, err := db.Exec(`UPDATE posts SET content=$1 WHERE post_id=$2`, content, postID)
	return err
}

// --- изображения в постах ---

func getImageIDsByPostID(db *sql.DB, postID int) ([]int, error) {
	const q = `SELECT image_id FROM post_images WHERE post_id=$1 ORDER BY image_id`
	rows, err := db.Query(q, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var i int
		if err := rows.Scan(&i); err != nil {
			return nil, err
		}
		ids = append(ids, i)
	}
	return ids, nil
}
func savePostImage(db *sql.DB, postID int, f multipart.File, h *multipart.FileHeader) error {
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	mt := h.Header.Get("Content-Type")
	if mt == "" {
		ext := filepath.Ext(h.Filename)
		mt = mime.TypeByExtension(ext)
		if mt == "" {
			mt = "application/octet-stream"
		}
	}
	const q = `INSERT INTO post_images (post_id,image_data,image_mime) VALUES($1,$2,$3)`
	_, err = db.Exec(q, postID, b, mt)
	return err
}
func deletePostImages(db *sql.DB, postID int) error {
	_, err := db.Exec(`DELETE FROM post_images WHERE post_id=$1`, postID)
	return err
}

// --- комментарии ---

func createComment(db *sql.DB, postID int, username, content string) (int, error) {
	const q = `INSERT INTO comments (post_id,username,content,created_at) VALUES($1,$2,$3,NOW()) RETURNING comment_id`
	var id int
	err := db.QueryRow(q, postID, username, content).Scan(&id)
	return id, err
}
func getCommentsByPostID(db *sql.DB, postID int) ([]Comment, error) {
	const q = `SELECT comment_id, post_id, username, content, created_at FROM comments WHERE post_id=$1 ORDER BY created_at`
	rows, err := db.Query(q, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cs []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.CommentID, &c.PostID, &c.Username, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		cs = append(cs, c)
	}
	return cs, nil
}
func getImageIDsByCommentID(db *sql.DB, commentID int) ([]int, error) {
	const q = `SELECT image_id FROM comment_images WHERE comment_id=$1 ORDER BY image_id`
	rows, err := db.Query(q, commentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var i int
		if err := rows.Scan(&i); err != nil {
			return nil, err
		}
		ids = append(ids, i)
	}
	return ids, nil
}
func saveCommentImage(db *sql.DB, commentID int, f multipart.File, h *multipart.FileHeader) error {
	b, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	mt := h.Header.Get("Content-Type")
	if mt == "" {
		ext := filepath.Ext(h.Filename)
		mt = mime.TypeByExtension(ext)
		if mt == "" {
			mt = "application/octet-stream"
		}
	}
	const q = `INSERT INTO comment_images (comment_id,image_data,image_mime) VALUES($1,$2,$3)`
	_, err = db.Exec(q, commentID, b, mt)
	return err
}

// --- HTTP-хендлеры ---

func mainFunc(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	var posts []Post
	var currentThread *Thread

	if tidStr := r.URL.Query().Get("thread_id"); tidStr != "" {
		if tid, err := strconv.Atoi(tidStr); err == nil {
			if th, err := getThreadByID(db, tid); err == nil {
				currentThread = &th
				posts, _ = getPostsByThreadID(db, tid)
			}
		}
	} else {
		posts, _ = getRecentPosts(db)
	}

	for i := range posts {
		posts[i].ImageIDs, _ = getImageIDsByPostID(db, posts[i].PostID)
		comments, _ := getCommentsByPostID(db, posts[i].PostID)
		for j := range comments {
			comments[j].ImageIDs, _ = getImageIDsByCommentID(db, comments[j].CommentID)
		}
		posts[i].Comments = comments
	}

	threads, _ := getAllThreads(db)
	data := TemplateData{Posts: posts, Threads: threads, CurrentThread: currentThread}

	tmpl := template.Must(template.ParseFiles("html/main.html"))
	if err := tmpl.ExecuteTemplate(w, "main", data); err != nil {
		http.Error(w, "Ошибка рендеринга", http.StatusInternalServerError)
	}
}

func addThreadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	title := r.FormValue("title")
	if title == "" {
		http.Error(w, "Название треда пусто", http.StatusBadRequest)
		return
	}
	db, _ := connectToDB()
	defer db.Close()
	createThread(db, title)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func addPostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	r.ParseMultipartForm(10 << 20)
	tid, _ := strconv.Atoi(r.FormValue("thread_id"))
	user := r.FormValue("username")
	content := r.FormValue("content")
	if user == "" || content == "" {
		http.Error(w, "Неверные поля формы для поста", http.StatusBadRequest)
		return
	}
	db, _ := connectToDB()
	defer db.Close()
	pid, _ := createPost(db, tid, user, content)
	if f, h, err := r.FormFile("image"); err == nil && h.Filename != "" {
		defer f.Close()
		savePostImage(db, pid, f, h)
	}
	http.Redirect(w, r, fmt.Sprintf("/?thread_id=%d", tid), http.StatusSeeOther)
}

func addCommentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	r.ParseMultipartForm(5 << 20)
	pid, _ := strconv.Atoi(r.FormValue("post_id"))
	user := r.FormValue("username")
	content := r.FormValue("content")
	if user == "" || content == "" {
		http.Error(w, "Неверные поля формы для комментария", http.StatusBadRequest)
		return
	}
	db, _ := connectToDB()
	defer db.Close()
	cid, _ := createComment(db, pid, user, content)
	if f, h, err := r.FormFile("image"); err == nil && h.Filename != "" {
		defer f.Close()
		saveCommentImage(db, cid, f, h)
	}
	redirect := "/"
	if r.FormValue("redirect_thread") == "1" {
		redirect = fmt.Sprintf("/?thread_id=%d", r.FormValue("thread_id"))
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func editPostFormHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	id, _ := strconv.Atoi(r.URL.Query().Get("post_id"))
	db, _ := connectToDB()
	defer db.Close()

	var p Post
	db.QueryRow(`SELECT post_id, thread_id, username, content FROM posts WHERE post_id=$1`, id).
		Scan(&p.PostID, &p.ThreadID, &p.Username, &p.Content)
	p.ImageIDs, _ = getImageIDsByPostID(db, p.PostID)

	tmpl := template.Must(template.ParseFiles("html/edit_post.html"))
	tmpl.ExecuteTemplate(w, "edit_post", p)
}

// POST /posts/edit
func editPostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	// multipart, чтобы получить файл
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Не удалось разобрать форму", http.StatusBadRequest)
		return
	}

	// Считываем поля
	postID, err := strconv.Atoi(r.FormValue("post_id"))
	if err != nil {
		http.Error(w, "Неверный post_id", http.StatusBadRequest)
		return
	}
	newContent := r.FormValue("content")
	if newContent == "" {
		http.Error(w, "Контент не может быть пустым", http.StatusBadRequest)
		return
	}

	db, err := connectToDB()
	if err != nil {
		http.Error(w, "Ошибка БД", http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Обновляем только текст
	if err := updatePostContent(db, postID, newContent); err != nil {
		http.Error(w, "Не удалось обновить текст", http.StatusInternalServerError)
		return
	}

	// Проверяем, загрузили ли новый файл
	file, header, err := r.FormFile("image")
	if err == nil && header.Filename != "" {
		// Удаляем старые картинки
		if delErr := deletePostImages(db, postID); delErr != nil {
			log.Printf("warn: не удалось удалить старые картинки для post_id=%d: %v", postID, delErr)
		}
		// Сохраняем новую
		defer file.Close()
		if saveErr := savePostImage(db, postID, file, header); saveErr != nil {
			log.Printf("warn: не удалось сохранить новую картинку для post_id=%d: %v", postID, saveErr)
		}
	}
	// иначе — ничего не трогаем с картинками, они остаются прежние

	// Узнаём thread_id, чтобы сделать редирект
	var threadID int
	_ = db.QueryRow(`SELECT thread_id FROM posts WHERE post_id=$1`, postID).Scan(&threadID)
	http.Redirect(w, r, fmt.Sprintf("/?thread_id=%d", threadID), http.StatusSeeOther)
}
func deletePostHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}
	r.ParseForm()
	id, _ := strconv.Atoi(r.FormValue("post_id"))
	db, _ := connectToDB()
	defer db.Close()
	db.Exec(`DELETE FROM posts WHERE post_id=$1`, id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func serveImageHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, _ := strconv.Atoi(vars["id"])
	db, _ := connectToDB()
	defer db.Close()

	var blob []byte
	var mt string
	err := db.QueryRow(`SELECT image_data, image_mime FROM post_images WHERE image_id=$1`, id).Scan(&blob, &mt)
	if err != nil {
		db.QueryRow(`SELECT image_data, image_mime FROM comment_images WHERE image_id=$1`, id).Scan(&blob, &mt)
	}

	w.Header().Set("Content-Type", mt)
	w.Header().Set("Content-Length", strconv.Itoa(len(blob)))
	w.Write(blob)
}

func randomThreadHandler(w http.ResponseWriter, r *http.Request) {
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

	thread, err := getRandomThread(db)
	if err != nil {
		http.Error(w, "Не удалось получить случайный тред", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/?thread_id=%d", thread.ThreadID), http.StatusSeeOther)
}

func main() {
	r := mux.NewRouter()
	r.PathPrefix("/css/").Handler(http.StripPrefix("/css/", http.FileServer(http.Dir("./css/"))))

	r.HandleFunc("/", mainFunc).Methods("GET")
	r.HandleFunc("/threads", addThreadHandler).Methods("POST")
	r.HandleFunc("/threads/random", randomThreadHandler).Methods("GET")
	r.HandleFunc("/posts", addPostHandler).Methods("POST")
	r.HandleFunc("/comments", addCommentHandler).Methods("POST")
	r.HandleFunc("/posts/edit", editPostFormHandler).Methods("GET")
	r.HandleFunc("/posts/edit", editPostHandler).Methods("POST")
	r.HandleFunc("/posts/delete", deletePostHandler).Methods("POST")
	r.HandleFunc("/images/{id}", serveImageHandler).Methods("GET")

	fmt.Println("Server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", r))
}
