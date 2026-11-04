sc.exe start LocalCPA
timeout /t 3 /nobreak >nul
sc.exe start LocalCPAManagerPlus
timeout /t 5 /nobreak >nul
sc.exe query LocalCPAManagerPlus
