package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var dbConn *gorm.DB

func initDB() {
	err := godotenv.Load()
	if err != nil {
		log.Println("Error loading .env file, using environment variables")
	}

	dbUser := os.Getenv("DB_USER")
	dbPass := os.Getenv("DB_PASS")
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbName := os.Getenv("DB_NAME")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local", dbUser, dbPass, dbHost, dbPort, dbName)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Println("Failed to connect to database:", err)
		return
	}
	dbConn = db
	fmt.Println("Database connected successfully!")
}

func main() {
	initDB()

	r := gin.Default()
	r.Use(cors.Default())

	// Home Route
	r.GET("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "success",
			"message": "Brainbird Gin Server is Running!",
		})
	})

	// Password Hash Route
	r.POST("/hash", func(c *gin.Context) {
		password := c.DefaultQuery("password", "Brainbird@123")
		hashedPassword, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		c.JSON(http.StatusOK, gin.H{
			"hash": string(hashedPassword),
		})
	})

	// Database Connection Check Route
	r.GET("/db-status", func(c *gin.Context) {
		if dbConn != nil {
			c.JSON(http.StatusOK, gin.H{"status": "Database is connected successfully!"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "Database connection failed."})
		}
	})

	r.Run(":8080")
}