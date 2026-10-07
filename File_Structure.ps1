# Create the root directory and navigate into it
New-Item -ItemType Directory -Path "atop-companion"
Set-Location -Path "atop-companion"

# Create the nested directory structure
New-Item -ItemType Directory -Path "ui", "telemetry", "storage", "reports"

# Create files in the root directory
New-Item -ItemType File -Path "go.mod", "go.sum", "main.go"

# Create files in the ui/ directory
New-Item -ItemType File -Path "ui\dashboard.go", "ui\components.go", "ui\modals.go"

# Create files in the telemetry/ directory
New-Item -ItemType File -Path "telemetry\engine.go", "telemetry\types.go"

# Create files in the storage/ directory
New-Item -ItemType File -Path "storage\bbolt_wrapper.go", "storage\guardrails.go"

# Create files in the reports/ directory
New-Item -ItemType File -Path "reports\svg_generator.go"
