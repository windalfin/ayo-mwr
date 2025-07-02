package api

import (
	"net/http"
	"strconv"
	"time"

	"ayo-mwr/database"

	"github.com/gin-gonic/gin"
)

// getBookings returns all bookings with optional filtering
func (s *Server) getBookings(c *gin.Context) {
	// Optional query parameters
	status := c.Query("status")
	date := c.Query("date")
	limit := c.DefaultQuery("limit", "100")
	
	limitInt, err := strconv.Atoi(limit)
	if err != nil || limitInt <= 0 {
		limitInt = 100
	}

	var bookings []database.BookingData
	
	if status != "" {
		bookings, err = s.db.GetBookingsByStatus(status)
	} else if date != "" {
		bookings, err = s.db.GetBookingsByDate(date)
	} else {
		// Get recent bookings (last 30 days)
		thirtyDaysAgo := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
		bookings, err = s.db.GetBookingsByDate(thirtyDaysAgo)
		// Note: This is a simplified implementation. 
		// For better performance, you might want to add a GetRecentBookings method
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to retrieve bookings",
			"details": err.Error(),
		})
		return
	}

	// Apply limit
	if len(bookings) > limitInt {
		bookings = bookings[:limitInt]
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"count": len(bookings),
		"data": bookings,
	})
}

// getBookingByID returns a specific booking by ID
func (s *Server) getBookingByID(c *gin.Context) {
	bookingID := c.Param("booking_id")
	
	if bookingID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Booking ID is required",
		})
		return
	}

	booking, err := s.db.GetBookingByID(bookingID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to retrieve booking",
			"details": err.Error(),
		})
		return
	}

	if booking == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "Booking not found",
			"booking_id": bookingID,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"data": booking,
	})
}

// getBookingsByStatus returns bookings filtered by status
func (s *Server) getBookingsByStatus(c *gin.Context) {
	status := c.Param("status")
	
	if status == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Status parameter is required",
		})
		return
	}

	bookings, err := s.db.GetBookingsByStatus(status)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to retrieve bookings by status",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"filter": gin.H{
			"status": status,
		},
		"count": len(bookings),
		"data": bookings,
	})
}

// getBookingsByDate returns bookings filtered by date
func (s *Server) getBookingsByDate(c *gin.Context) {
	date := c.Param("date")
	
	if date == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Date parameter is required (format: YYYY-MM-DD)",
		})
		return
	}

	// Validate date format
	_, err := time.Parse("2006-01-02", date)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid date format. Use YYYY-MM-DD",
			"example": "2025-01-23",
		})
		return
	}

	bookings, err := s.db.GetBookingsByDate(date)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to retrieve bookings by date",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"filter": gin.H{
			"date": date,
		},
		"count": len(bookings),
		"data": bookings,
	})
} 