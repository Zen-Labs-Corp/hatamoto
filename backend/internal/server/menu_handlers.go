package server

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"hatamoto.dev/core/internal/menu"
)

// handleListMenus returns all menus for the tenant.
func (s *Server) handleListMenus(w http.ResponseWriter, r *http.Request) {
	menus, err := s.menuService.ListMenus(r.Context())
	if err != nil {
		s.logger.Error("failed to list menus", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list menus")
		return
	}

	// Ensure empty array instead of null
	if menus == nil {
		menus = []*menu.Menu{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"menus": menus,
		"total": len(menus),
	})
}

// handleCreateMenu creates a new menu.
func (s *Server) handleCreateMenu(w http.ResponseWriter, r *http.Request) {

	var input menu.CreateMenuInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	m, err := s.menuService.CreateMenu(r.Context(), &input)
	s.invalidateRendered(r.Context()) // menus are part of every rendered page
	if err != nil {
		if errors.Is(err, menu.ErrSlugAlreadyExists) {
			writeError(w, http.StatusConflict, "menu slug already exists")
			return
		}
		if errors.Is(err, menu.ErrInvalidLocation) {
			writeError(w, http.StatusBadRequest, "invalid menu location")
			return
		}
		s.logger.Error("failed to create menu", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create menu")
		return
	}

	writeJSON(w, http.StatusCreated, m)
}

// handleGetMenu retrieves a menu by ID with all items.
func (s *Server) handleGetMenu(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu ID")
		return
	}

	m, err := s.menuService.GetMenuWithItems(r.Context(), id)
	if err != nil {
		if errors.Is(err, menu.ErrMenuNotFound) {
			writeError(w, http.StatusNotFound, "menu not found")
			return
		}
		s.logger.Error("failed to get menu", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get menu")
		return
	}

	writeJSON(w, http.StatusOK, m)
}

// handleUpdateMenu modifies a menu.
func (s *Server) handleUpdateMenu(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu ID")
		return
	}

	var input menu.UpdateMenuInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	m, err := s.menuService.UpdateMenu(r.Context(), id, &input)
	s.invalidateRendered(r.Context()) // menus are part of every rendered page
	if err != nil {
		if errors.Is(err, menu.ErrMenuNotFound) {
			writeError(w, http.StatusNotFound, "menu not found")
			return
		}
		if errors.Is(err, menu.ErrInvalidLocation) {
			writeError(w, http.StatusBadRequest, "invalid menu location")
			return
		}
		s.logger.Error("failed to update menu", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update menu")
		return
	}

	writeJSON(w, http.StatusOK, m)
}

// handleDeleteMenu removes a menu.
func (s *Server) handleDeleteMenu(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu ID")
		return
	}

	err = s.menuService.DeleteMenu(r.Context(), id)
	s.invalidateRendered(r.Context()) // menus are part of every rendered page
	if err != nil {
		if errors.Is(err, menu.ErrMenuNotFound) {
			writeError(w, http.StatusNotFound, "menu not found")
			return
		}
		s.logger.Error("failed to delete menu", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete menu")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "menu deleted"})
}

// handleCreateMenuItem adds an item to a menu.
func (s *Server) handleCreateMenuItem(w http.ResponseWriter, r *http.Request) {

	idStr := r.PathValue("id")
	menuID, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu ID")
		return
	}

	var input menu.CreateMenuItemInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if input.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}

	if input.LinkType == "" {
		writeError(w, http.StatusBadRequest, "link_type is required")
		return
	}

	item, err := s.menuService.CreateMenuItem(r.Context(), menuID, &input)
	s.invalidateRendered(r.Context()) // menus are part of every rendered page
	if err != nil {
		if errors.Is(err, menu.ErrMenuNotFound) {
			writeError(w, http.StatusNotFound, "menu not found")
			return
		}
		if errors.Is(err, menu.ErrMenuItemNotFound) {
			writeError(w, http.StatusBadRequest, "parent item not found")
			return
		}
		if errors.Is(err, menu.ErrInvalidLinkType) {
			writeError(w, http.StatusBadRequest, "invalid link type")
			return
		}
		s.logger.Error("failed to create menu item", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create menu item")
		return
	}

	writeJSON(w, http.StatusCreated, item)
}

// handleUpdateMenuItem modifies a menu item.
func (s *Server) handleUpdateMenuItem(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	_, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu ID")
		return
	}

	itemIDStr := r.PathValue("itemId")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid item ID")
		return
	}

	var input menu.UpdateMenuItemInput
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	item, err := s.menuService.UpdateMenuItem(r.Context(), itemID, &input)
	s.invalidateRendered(r.Context()) // menus are part of every rendered page
	if err != nil {
		if errors.Is(err, menu.ErrMenuItemNotFound) {
			writeError(w, http.StatusNotFound, "menu item not found")
			return
		}
		if errors.Is(err, menu.ErrInvalidLinkType) {
			writeError(w, http.StatusBadRequest, "invalid link type")
			return
		}
		if errors.Is(err, menu.ErrCircularParent) {
			writeError(w, http.StatusBadRequest, "circular parent reference not allowed")
			return
		}
		s.logger.Error("failed to update menu item", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update menu item")
		return
	}

	writeJSON(w, http.StatusOK, item)
}

// handleDeleteMenuItem removes a menu item.
func (s *Server) handleDeleteMenuItem(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	_, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu ID")
		return
	}

	itemIDStr := r.PathValue("itemId")
	itemID, err := uuid.Parse(itemIDStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid item ID")
		return
	}

	err = s.menuService.DeleteMenuItem(r.Context(), itemID)
	s.invalidateRendered(r.Context()) // menus are part of every rendered page
	if err != nil {
		if errors.Is(err, menu.ErrMenuItemNotFound) {
			writeError(w, http.StatusNotFound, "menu item not found")
			return
		}
		s.logger.Error("failed to delete menu item", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete menu item")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "menu item deleted"})
}

// handleReorderMenuItems updates positions and parent relationships.
func (s *Server) handleReorderMenuItems(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	menuID, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid menu ID")
		return
	}

	var input struct {
		Items []menu.ReorderInput `json:"items"`
	}
	if err := parseJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(input.Items) == 0 {
		writeError(w, http.StatusBadRequest, "items array is required")
		return
	}

	err = s.menuService.ReorderItems(r.Context(), menuID, input.Items)
	s.invalidateRendered(r.Context()) // menus are part of every rendered page
	if err != nil {
		if errors.Is(err, menu.ErrMenuNotFound) {
			writeError(w, http.StatusNotFound, "menu not found")
			return
		}
		s.logger.Error("failed to reorder menu items", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to reorder menu items")
		return
	}

	// Return the updated menu with items
	m, err := s.menuService.GetMenuWithItems(r.Context(), menuID)
	if err != nil {
		s.logger.Error("failed to get updated menu", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get updated menu")
		return
	}

	writeJSON(w, http.StatusOK, m)
}
