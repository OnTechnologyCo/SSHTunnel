# Shared by build and installer packaging, including -SkipBuild.
function Get-ProjectGoToolchain {
    $root = Split-Path $PSScriptRoot -Parent
    $version = (Get-Content -LiteralPath (Join-Path $root '.go-version') -Raw).Trim()
    if ($version -notmatch '^\d+\.\d+\.\d+$') { throw 'Invalid .go-version.' }
    $module = Get-Content -LiteralPath (Join-Path $root 'go.mod') -Raw
    if ($module -notmatch ('(?m)^go ' + [regex]::Escape($version) + '\s*$')) {
        throw 'go.mod and .go-version must specify the same patched Go version.'
    }
    return ('go' + $version)
}

function Assert-ProjectGoBinary([string]$Path) {
    $expected = Get-ProjectGoToolchain
    $metadata = @(& go version -m $Path 2>&1)
    if ($LASTEXITCODE -ne 0 -or $metadata.Count -eq 0 -or
        $metadata[0] -notmatch (': ' + [regex]::Escape($expected) + '$')) {
        throw "Refusing to package $Path; rebuild with $expected."
    }
    return $metadata
}
