Option Explicit
Dim fs, shell, root, exe, quote
Set fs = CreateObject("Scripting.FileSystemObject")
Set shell = CreateObject("WScript.Shell")
root = fs.GetParentFolderName(WScript.ScriptFullName)
exe = fs.BuildPath(root, "SSHTunnelServer.exe")
If Not fs.FileExists(exe) Then exe = fs.BuildPath(root, "output\SSHTunnelServer.exe")
If Not fs.FileExists(exe) Then WScript.Quit 1
quote = Chr(34)
shell.Run quote & exe & quote & " tray --data-dir " & quote & fs.BuildPath(root, "data") & quote, 0, False
