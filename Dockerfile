# ---- Build Stage ----
# Use a specific Go version for reproducibility. 'alpine' is a lightweight Linux distro.
FROM golang:1.25-alpine AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy go.mod and go.sum files to leverage Docker's layer caching.
# This step is separate so dependencies are only re-downloaded if go.mod/go.sum change.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code into the container
COPY internal .

# Build the Go binary.
# CGO_ENABLED=0 creates a static binary without any C dependencies.
# -ldflags="-w -s" strips debug information, reducing the binary size.
RUN CGO_ENABLED=0 go build -ldflags="-w -s" -o /go-deployment-reporter .

# ---- Run Stage ----
# Use a minimal, secure base image. 'distroless' contains only your app and its runtime dependencies.
# It has no shell, package manager, or other programs, which is great for security.
FROM gcr.io/distroless/static-debian11

# Set the working directory
WORKDIR /

# Copy the compiled binary from the 'builder' stage
COPY --from=builder /go-deployment-reporter /go-deployment-reporter

# Expose the port the application listens on.
# This is documentation for the user; it doesn't actually publish the port.
EXPOSE 8080

# Set a non-root user for security best practices.
USER nonroot:nonroot

# Define the command to run the application when the container starts.
# The application is configured via environment variables passed by Kubernetes.
CMD ["/go-deployment-reporter"]