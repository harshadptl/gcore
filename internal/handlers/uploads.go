package handlers

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gofiber/fiber/v3"

	"github.com/harshadptl/gcore/internal/store"
)

// allowedImageTypes maps sniffed MIME types to file extensions.
var allowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// attachCover saves an uploaded "cover_file" (if any) and points
// in.CoverImage at it. Validation problems are reported through errs.
func (h *Handler) attachCover(c fiber.Ctx, in *store.PostInput, errs map[string]string) error {
	fh, err := c.FormFile("cover_file")
	if err != nil || fh == nil || fh.Size == 0 {
		return nil // no file uploaded
	}
	if fh.Size > h.Cfg.MaxUploadBytes {
		errs["cover_image"] = fmt.Sprintf("Images must be smaller than %d MB.", h.Cfg.MaxUploadBytes>>20)
		return nil
	}
	url, err := h.saveImage(fh, "covers")
	if err != nil {
		if errors.Is(err, errUnsupportedImage) {
			errs["cover_image"] = "Upload a JPEG, PNG, GIF or WebP image."
			return nil
		}
		return err
	}
	in.CoverImage = url
	return nil
}

var errUnsupportedImage = errors.New("unsupported image type")

// saveImage stores an uploaded image under UPLOAD_DIR/<dir>/ with a random
// name and returns its public URL. The type is detected from the bytes, not
// the client-supplied filename or Content-Type.
func (h *Handler) saveImage(fh *multipart.FileHeader, dir string) (string, error) {
	src, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	head := make([]byte, 512)
	n, err := io.ReadFull(src, head)
	if err != nil && err != io.ErrUnexpectedEOF {
		return "", err
	}
	ext, ok := allowedImageTypes[http.DetectContentType(head[:n])]
	if !ok {
		return "", errUnsupportedImage
	}

	targetDir := filepath.Join(h.Cfg.UploadDir, dir)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return "", err
	}
	name := store.NewToken()[:24] + ext
	dst, err := os.OpenFile(filepath.Join(targetDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := dst.Write(head[:n]); err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, io.LimitReader(src, h.Cfg.MaxUploadBytes)); err != nil {
		return "", err
	}
	return "/uploads/" + dir + "/" + name, nil
}
