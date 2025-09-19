package api

import (
	"ayo-mwr/database"
	"log"
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
			log.Printf("🔍 AUTH: Authentication required for path '%s'", c.Request.URL.Path)
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
		
		// Only log authentication for non-API paths or first time after login
		// Reduce log noise for frequent API calls
		if !strings.HasPrefix(c.Request.URL.Path, "/api") {
			log.Printf("✅ AUTH: User authenticated, allowing access to '%s'", c.Request.URL.Path)
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
	err = session.Save()
	if err != nil {
		c.Header("Content-Type", "text/html")
		c.String(http.StatusInternalServerError, s.getRegisterHTML()+"<script>alert('Session error: "+err.Error()+"');</script>")
		return
	}

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
		log.Printf("❌ AUTH: Database error getting user '%s': %v", username, err)
		c.Header("Content-Type", "text/html")
		c.String(http.StatusInternalServerError, s.getLoginHTML()+"<script>alert('Database error');</script>")
		return
	}

	if user == nil {
		log.Printf("❌ AUTH: User '%s' not found", username)
		c.Header("Content-Type", "text/html")
		c.String(http.StatusUnauthorized, s.getLoginHTML()+"<script>alert('Invalid username or password');</script>")
		return
	}

	if !database.ValidatePassword(user.PasswordHash, password) {
		log.Printf("❌ AUTH: Invalid password for user '%s'", username)
		c.Header("Content-Type", "text/html")
		c.String(http.StatusUnauthorized, s.getLoginHTML()+"<script>alert('Invalid username or password');</script>")
		return
	}

	log.Printf("✅ AUTH: User '%s' authenticated successfully", username)
	
	// Create session
	session := sessions.Default(c)
	session.Set("user_id", user.ID)
	session.Set("username", user.Username)
	log.Printf("🔐 AUTH: Creating session for user '%s' (ID: %d)", user.Username, user.ID)
	err = session.Save()
	if err != nil {
		log.Printf("❌ AUTH: Session save error for user '%s': %v", username, err)
		c.Header("Content-Type", "text/html")
		c.String(http.StatusInternalServerError, s.getLoginHTML()+"<script>alert('Session error: "+err.Error()+"');</script>")
		return
	}

	log.Printf("✅ AUTH: Session created successfully for user '%s', redirecting to dashboard", user.Username)
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

// ChangePassword handles password change requests
func (s *Server) handleChangePassword(c *gin.Context) {
	session := sessions.Default(c)
	userID := session.Get("user_id")
	username := session.Get("username")
	
	if userID == nil || username == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
		return
	}
	
	var req struct {
		CurrentPassword string `json:"currentPassword" binding:"required"`
		NewPassword     string `json:"newPassword" binding:"required"`
	}
	
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	
	// Validate new password length
	if len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "New password must be at least 8 characters long"})
		return
	}
	
	// Get user from database
	user, err := s.db.GetUserByUsername(username.(string))
	if err != nil || user == nil {
		log.Printf("❌ AUTH: Error getting user for password change: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}
	
	// Verify current password
	if !database.ValidatePassword(user.PasswordHash, req.CurrentPassword) {
		log.Printf("❌ AUTH: Invalid current password for user '%s' during password change", username.(string))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Current password is incorrect"})
		return
	}
	
	// Update password in database
	err = s.db.UpdateUserPassword(user.ID, req.NewPassword)
	if err != nil {
		log.Printf("❌ AUTH: Error updating password for user '%s': %v", username.(string), err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}
	
	log.Printf("✅ AUTH: Password changed successfully for user '%s'", username.(string))
	c.JSON(http.StatusOK, gin.H{"message": "Password changed successfully"})
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
        <div class="footer">
            <p>You can change your password after logging in</p>
        </div>
    </div>
</body>
</html>`
}