package api

import (
	"ayo-mwr/database"
	"net/http"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// AuthMiddleware checks if user is authenticated
func (s *Server) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		session := sessions.Default(c)
		userID := session.Get("user_id")
		
		if userID == nil {
			// Check if this is an API request
			if c.Request.Header.Get("Accept") == "application/json" || 
				c.GetHeader("Content-Type") == "application/json" ||
				strings.HasPrefix(c.Request.URL.Path, "/api") {
				// Return JSON error for API requests
				c.JSON(http.StatusUnauthorized, gin.H{
					"error": "Authentication required",
					"redirect": "/login",
				})
			} else {
				// For root path, check if we need to redirect to register first
				if c.Request.URL.Path == "/" {
					// Check if users exist in the system
					hasUsers, err := s.db.HasUsers()
					if err == nil && !hasUsers {
						c.Redirect(http.StatusFound, "/register")
						c.Abort()
						return
					}
				}
				// Redirect to login page for HTML requests
				c.Redirect(http.StatusFound, "/login")
			}
			c.Abort()
			return
		}
		
		c.Next()
	}
}

// Register handles user registration
func (s *Server) handleRegister(c *gin.Context) {
	if c.Request.Method == "GET" {
		// Check if users already exist
		hasUsers, err := s.db.HasUsers()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}
		
		if hasUsers {
			// Redirect to login if users already exist
			c.Redirect(http.StatusFound, "/login")
			return
		}
		
		// Serve registration page
		c.Header("Content-Type", "text/html")
		c.String(http.StatusOK, s.getRegisterHTML())
		return
	}
	
	// Handle POST request
	username := c.PostForm("username")
	password := c.PostForm("password")
	confirmPassword := c.PostForm("confirm_password")
	
	if username == "" || password == "" || confirmPassword == "" {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusBadRequest, s.getRegisterHTML()+"<script>alert('All fields are required');</script>")
		return
	}
	
	if password != confirmPassword {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusBadRequest, s.getRegisterHTML()+"<script>alert('Passwords do not match');</script>")
		return
	}
	
	if len(password) < 6 {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusBadRequest, s.getRegisterHTML()+"<script>alert('Password must be at least 6 characters long');</script>")
		return
	}
	
	// Check if users already exist
	hasUsers, err := s.db.HasUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	
	if hasUsers {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	
	// Create user
	err = s.db.CreateUser(username, password)
	if err != nil {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusBadRequest, s.getRegisterHTML()+"<script>alert('Error creating user: "+err.Error()+"');</script>")
		return
	}
	
	// Auto-login the user
	user, err := s.db.GetUserByUsername(username)
	if err != nil || user == nil {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	
	session := sessions.Default(c)
	session.Set("user_id", user.ID)
	session.Set("username", user.Username)
	session.Save()
	
	// Redirect to dashboard
	c.Redirect(http.StatusFound, "/")
}

// Login handles user authentication
func (s *Server) handleLogin(c *gin.Context) {
	if c.Request.Method == "GET" {
		// Check if no users exist, redirect to register
		hasUsers, err := s.db.HasUsers()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}
		
		if !hasUsers {
			c.Redirect(http.StatusFound, "/register")
			return
		}
		
		// Serve login page
		c.Header("Content-Type", "text/html")
		c.String(http.StatusOK, s.getLoginHTML())
		return
	}
	
	// Handle POST request
	username := c.PostForm("username")
	password := c.PostForm("password")
	
	if username == "" || password == "" {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusBadRequest, s.getLoginHTML()+"<script>alert('Username and password are required');</script>")
		return
	}
	
	// Get user from database
	user, err := s.db.GetUserByUsername(username)
	if err != nil {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusInternalServerError, s.getLoginHTML()+"<script>alert('Database error');</script>")
		return
	}
	
	if user == nil || !database.ValidatePassword(user.PasswordHash, password) {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusUnauthorized, s.getLoginHTML()+"<script>alert('Invalid username or password');</script>")
		return
	}
	
	// Create session
	session := sessions.Default(c)
	session.Set("user_id", user.ID)
	session.Set("username", user.Username)
	session.Save()
	
	// Redirect to dashboard
	c.Redirect(http.StatusFound, "/")
}

// Logout handles user logout
func (s *Server) handleLogout(c *gin.Context) {
	session := sessions.Default(c)
	session.Clear()
	session.Save()
	
	c.Redirect(http.StatusFound, "/login")
}

// getRegisterHTML returns the registration page HTML
func (s *Server) getRegisterHTML() string {
	return `
<!DOCTYPE html>
<html>
<head>
    <title>AYO MWR - Setup Admin Account</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 0; padding: 0; background: #f5f5f5; }
        .container { max-width: 400px; margin: 100px auto; padding: 20px; background: white; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        .header { text-align: center; margin-bottom: 30px; }
        .header h1 { color: #333; margin: 0; }
        .header p { color: #666; margin: 10px 0 0 0; }
        .form-group { margin-bottom: 15px; }
        .form-group label { display: block; margin-bottom: 5px; color: #333; }
        .form-group input { width: 100%; padding: 10px; border: 1px solid #ddd; border-radius: 4px; box-sizing: border-box; }
        .btn { width: 100%; padding: 12px; background: #007bff; color: white; border: none; border-radius: 4px; cursor: pointer; font-size: 16px; }
        .btn:hover { background: #0056b3; }
        .footer { text-align: center; margin-top: 20px; color: #666; font-size: 14px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>AYO MWR</h1>
            <p>Setup Admin Account</p>
        </div>
        <form method="POST">
            <div class="form-group">
                <label for="username">Username</label>
                <input type="text" id="username" name="username" required>
            </div>
            <div class="form-group">
                <label for="password">Password</label>
                <input type="password" id="password" name="password" required minlength="6">
            </div>
            <div class="form-group">
                <label for="confirm_password">Confirm Password</label>
                <input type="password" id="confirm_password" name="confirm_password" required minlength="6">
            </div>
            <button type="submit" class="btn">Create Admin Account</button>
        </form>
        <div class="footer">
            First time setup - create your administrator account
        </div>
    </div>
</body>
</html>`
}

// getLoginHTML returns the login page HTML
func (s *Server) getLoginHTML() string {
	return `
<!DOCTYPE html>
<html>
<head>
    <title>AYO MWR - Login</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 0; padding: 0; background: #f5f5f5; }
        .container { max-width: 400px; margin: 100px auto; padding: 20px; background: white; border-radius: 8px; box-shadow: 0 2px 10px rgba(0,0,0,0.1); }
        .header { text-align: center; margin-bottom: 30px; }
        .header h1 { color: #333; margin: 0; }
        .header p { color: #666; margin: 10px 0 0 0; }
        .form-group { margin-bottom: 15px; }
        .form-group label { display: block; margin-bottom: 5px; color: #333; }
        .form-group input { width: 100%; padding: 10px; border: 1px solid #ddd; border-radius: 4px; box-sizing: border-box; }
        .btn { width: 100%; padding: 12px; background: #007bff; color: white; border: none; border-radius: 4px; cursor: pointer; font-size: 16px; }
        .btn:hover { background: #0056b3; }
        .footer { text-align: center; margin-top: 20px; color: #666; font-size: 14px; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>AYO Video</h1>
            <p>Admin Dashboard Login</p>
        </div>
        <form method="POST">
            <div class="form-group">
                <label for="username">Username</label>
                <input type="text" id="username" name="username" required>
            </div>
            <div class="form-group">
                <label for="password">Password</label>
                <input type="password" id="password" name="password" required>
            </div>
            <button type="submit" class="btn">Login</button>
        </form>
    </div>
</body>
</html>`
}