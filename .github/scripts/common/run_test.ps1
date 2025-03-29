function Run-OneTest {
    param (
        [string]$test
    )

    $testName = $test -replace '\(.*$',''
    
    Write-Host "Start Test: $testName" -ForegroundColor Blue
    $startTime = Get-Date
    
    try {
        & $test
        $exitStatus = 0
    } catch {
        $exitStatus = 1
        Write-Host "Test Error: $_" -ForegroundColor Red
    }

    $endTime = Get-Date
    $elapsed = ($endTime - $startTime).TotalSeconds

    if ($exitStatus -eq 0) {
        Write-Host "Finish Test: $testName in ${elapsed} seconds" -ForegroundColor Blue
    } else {
        Write-Host "Test Failed: $testName($($MyInvocation.MyCommand.Name)) in ${elapsed} seconds" -ForegroundColor Red
        exit 1
    }
}

function Run-Test {
    param (
        [string[]]$Tests
    )

    $allStartTime = Get-Date

    if ($Tests) {
        foreach ($test in $Tests) {
            if (Get-Command $test -ErrorAction SilentlyContinue) {
                Run-OneTest $test
            } else {
                Write-Host "Test $test was not found" -ForegroundColor Red
                exit 1
            }
        }
    } else {
        $tests = Get-ChildItem function:test_* | Select-Object -ExpandProperty Name
        if (-not $tests) {
            Write-Host "No test function found" -ForegroundColor Red
            exit 1
        }

        foreach ($test in $tests) {
            Run-OneTest $test
        }
    }

    $allElapsed = (Get-Date - $allStartTime).TotalSeconds
    Write-Host "All tests passed in ${allElapsed} seconds" -ForegroundColor Blue
}