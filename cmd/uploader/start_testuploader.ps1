param([int]$jobs)

$rootUpload = (git.exe rev-parse --show-toplevel)
$uploader = "$rootUpload/cmd/uploader"


Start-Job -Name j1 -ArgumentList $uploader -ScriptBlock {
	
	param($uploader)
	
	[Console]::OutputEncoding = [System.Text.Encoding]::GetEncoding("utf-8")
	
	"`$uploader=$uploader"
	
	Set-Location $uploader -PassThru;
	
	.\uploader.exe -service https://127.0.0.1:64000/upload -username testuser -dir ./testdata/testbackups -passwordfile ./testdata/testlogin.json -cacert ./testdata/mkcertCA.pem -skipmarkAsUploaded; 

}
if ($jobs -gt 0) {
	Write-Host "parallel jobs $jobs"
	Start-Job -Name j2 -ArgumentList $uploader -ScriptBlock {
		
		param($uploader)
		
		[Console]::OutputEncoding = [System.Text.Encoding]::GetEncoding("utf-8")
		
		Set-Location $uploader;
		#different directory 2
		.\uploader.exe -service https://127.0.0.1:64000/upload -username testuser2 -dir ./testdata/testbackups2 -passwordfile ./testdata/testlogin.json -cacert ./testdata/mkcertCA.pem -skipmarkAsUploaded; 
		
	}
}

Receive-Job -Name j1 -AutoRemoveJob -Wait
Receive-Job -Name j2 -AutoRemoveJob -Wait