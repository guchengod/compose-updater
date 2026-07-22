$ErrorActionPreference = "Stop"

$App = "compose-updater"
$Version = if ($env:VERSION) { $env:VERSION } else { "dev" }
$Commit = if ($env:COMMIT) { $env:COMMIT } else { "none" }
$BuildDate = if ($env:BUILD_DATE) { $env:BUILD_DATE } else { (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ") }
$Ldflags = "-s -w -X main.version=$Version -X main.commit=$Commit -X main.buildDate=$BuildDate"

Remove-Item -Recurse -Force build -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path build | Out-Null

$Targets = @(
    @{ OS = "linux";  Arch = "amd64"; Ext = "" },
    @{ OS = "linux";  Arch = "arm64"; Ext = "" },
    @{ OS = "darwin"; Arch = "amd64"; Ext = "" },
    @{ OS = "darwin"; Arch = "arm64"; Ext = "" },
    @{ OS = "windows"; Arch = "amd64"; Ext = ".exe" },
    @{ OS = "windows"; Arch = "arm64"; Ext = ".exe" }
)

foreach ($Target in $Targets) {
    $Output = "build/$App-$($Target.OS)-$($Target.Arch)$($Target.Ext)"
    Write-Host "building $Output"
    $env:CGO_ENABLED = "0"
    $env:GOOS = $Target.OS
    $env:GOARCH = $Target.Arch
    go build -trimpath -ldflags $Ldflags -o $Output ./cmd/compose-updater
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

Get-ChildItem build/compose-updater-* | ForEach-Object {
    $Hash = (Get-FileHash -Algorithm SHA256 $_.FullName).Hash.ToLowerInvariant()
    "$Hash  $($_.Name)"
} | Set-Content -Encoding ascii build/SHA256SUMS
