# debug.ps1
param (
    [string]$META_URL = "redis://127.0.0.1:6379/1"
)

function Prepare-WinTest {
    try { Start-Service -Name "redisredis" } catch { Write-Host "Redis service may not exist" }

    try { .\juicefs.exe umount z: } catch { Write-Host "Unmount failed or not mounted" }
    
    Remove-Item -Path "C:\jfs\local\myjfs\" -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -Path "C:\jfsCache\local\myjfs\" -Recurse -Force -ErrorAction SilentlyContinue

    try {
        $uuid = (.\juicefs.exe status $META_URL | Select-String -Pattern 'UUID' | ForEach-Object { 
            $_.Line.Split('"')[1] 
        })
        if ($uuid) {
            .\juicefs.exe destroy --force $META_URL $uuid
        }
    } catch { Write-Host "Destroy failed" }

    try { 
        redis-cli -h 127.0.0.1 -p 6379 -n 1 FLUSHDB 
    } catch { Write-Host "Redis FLUSHDB failed" }
}

function Compare-MD5 {
    param (
        [string]$file1,
        [string]$file2
    )

    $md51 = (Get-FileHash -Path $file1 -Algorithm MD5).Hash.ToLower()
    $md52 = (Get-FileHash -Path $file2 -Algorithm MD5).Hash.ToLower()

    if ($md51 -ne $md52) {
        Write-Host "MD5 are different: $md51 vs $md52" -ForegroundColor Red
        exit 1
    }
}

function Check-DebugFile {
    $files = @("system-info.log", "juicefs.log", "config.txt", "stats.txt", "stats.5s.txt")
    $debugDir = "debug"
    
    if (-not (Test-Path $debugDir)) {
        Write-Error "error:no debug dir"
        exit 1
    }

    $allFilesExist = $true
    foreach ($file in $files) {
        $found = Get-ChildItem -Path $debugDir -Filter $file -Recurse | Measure-Object
        if ($found.Count -eq 0) {
            Write-Output "no $file"
            $allFilesExist = $false
        }
    }

    if ($allFilesExist) {
        Write-Output "pass"
    } else {
        exit 1
    }
}

function Test-DebugJuicefs {
    Prepare-WinTest
    .\juicefs.exe format $META_URL myjfs
    .\juicefs.exe mount -d $META_URL z:
    
    $fileSize = 1GB  # 1024MB
    $outFile = "z:\bigfile"
    fsutil file createnew $outFile $fileSize
    
    .\juicefs.exe debug z:
    Check-DebugFile
    
    tree /F debug
    
    .\juicefs.exe rmr z:\bigfile
}

function Test-DebugAbnormalJuicefs {
    Remove-Item -Path "debug" -Recurse -Force -ErrorAction SilentlyContinue
    Prepare-WinTest
    .\juicefs.exe format $META_URL myjfs
    .\juicefs.exe mount -d $META_URL z:
    
    $fileSize = 1GB
    $outFile = "z:\bigfile"
    fsutil file createnew $outFile $fileSize
    
    Stop-Process -Name "redis-server" -Force -ErrorAction SilentlyContinue
    
    .\juicefs.exe debug z:
    Check-DebugFile
    .\juicefs.exe rmr z:\bigfile
}

Test-DebugJuicefs
function Invoke-TestSuite {
    $testFunctions = Get-ChildItem Function:test_* | 
                    Where-Object { $_.ScriptBlock.Attributes -notcontains 'Hidden' }
    
    if (-not $testFunctions) {
        Write-Host "##[warning]no test found"
        return
    }

    foreach ($test in $testFunctions.Name) {
        try {
            Write-Host "##[group]Running test: $test"
            & $test
            Write-Host "##[endgroup]"
            Write-Host "##[section]$test - PASSED" -ForegroundColor Green
        }
        catch {
            Write-Host "##[error]$test - FAILED: $_" -ForegroundColor Red
            Write-Host "##[endgroup]"
            exit 1
        }
    }
}

