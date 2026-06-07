# DDNS

轻量 DDNS 客户端：获取当前设备公网出网 IP，查询域名当前 A 记录，不一致时更新 A 记录。

当前实现：

- Go 单文件可执行程序
- `check-ip` / `once` / `run`
- 通过项目目录中的 `nssm.exe` 安装/卸载/启动/停止 Windows 服务
- JSON 配置
- 腾讯云 DNSPod Provider
- 阿里云 DNS Provider
- 日志按 2000 行轮转，最多保留 5 个日志文件
- JSON 状态文件
- 无第三方 Go 依赖

## 目录

```text
DDNS/
  src/       源码和 Go module
  runtime/   运行目录：ddns.exe、config.json、nssm.exe
  output/    输出目录：日志和状态文件
```

## 构建

```powershell
cd .\src
go build -o ..\runtime\ddns.exe .\cmd\ddns
```

## 配置

复制 `runtime\config.example.json` 为 `runtime\config.json`，然后修改 `records` 中的域名和 Provider。

同一份配置支持腾讯云和阿里云。启用哪个 Provider，只改 `records[].provider`。

腾讯云 DNSPod 示例：

```json
{
  "records": [
    {
      "provider": "dnspod",
      "zone": "example.com",
      "name": "home.example.com",
      "type": "A",
      "ttl": 600,
      "record_line": "默认"
    }
  ],
  "providers": {
    "dnspod": {
      "secret_id_env": "TENCENTCLOUD_SECRET_ID",
      "secret_key_env": "TENCENTCLOUD_SECRET_KEY"
    },
    "aliyun": {
      "access_key_id_env": "ALIYUN_ACCESS_KEY_ID",
      "access_key_secret_env": "ALIYUN_ACCESS_KEY_SECRET"
    }
  }
}
```

阿里云只需要把 `records[].provider` 改成 `aliyun`，并配置阿里云密钥字段。

日志默认写入 `output\logs\ddns.log`。单个日志文件超过 `2000` 行后会轮转为 `ddns.1.log`，最多保留 `5` 个日志文件：

```json
"logging": {
  "level": "INFO",
  "file": "..\\output\\logs\\ddns.log",
  "max_lines": 2000,
  "max_files": 5
}
```

腾讯云 DNSPod：

```powershell
$env:TENCENTCLOUD_SECRET_ID="your-secret-id"
$env:TENCENTCLOUD_SECRET_KEY="your-secret-key"
```

阿里云 DNS：

```powershell
$env:ALIYUN_ACCESS_KEY_ID="your-access-key-id"
$env:ALIYUN_ACCESS_KEY_SECRET="your-access-key-secret"
```

也可以直接在 `config.json` 写 `secret_id` / `secret_key`，或 `access_key_id` / `access_key_secret`。更推荐环境变量，避免误提交密钥。

## 手动调试

只查看当前公网出网 IP：

```powershell
.\runtime\ddns.exe check-ip -config .\runtime\config.json
```

执行一次同步：

```powershell
.\runtime\ddns.exe once -config .\runtime\config.json
```

## Windows 服务

`runtime` 目录需要有 `nssm.exe`。

用管理员 PowerShell 安装服务：

```powershell
.\runtime\ddns.exe install-service -config .\runtime\config.json
```

如果已经安装过，再执行一次 `install-service` 会刷新 NSSM 参数。

安装后 NSSM 会托管：

```powershell
.\runtime\ddns.exe service -config .\runtime\config.json
```

安装时可以传相对路径；程序注册到 NSSM 前会转成绝对路径，避免服务启动时找不到配置。

启动服务：

```powershell
.\runtime\ddns.exe start-service
```

停止服务：

```powershell
.\runtime\ddns.exe stop-service
```

卸载服务：

```powershell
.\runtime\ddns.exe uninstall-service
```

重复执行卸载是安全的；服务不存在时也会直接返回成功。

服务模式会按 `interval_seconds` 常驻轮询。

如果调整过目录后服务启动失败，先用管理员 PowerShell 重新刷新服务配置：

```powershell
.\runtime\ddns.exe install-service -config .\runtime\config.json
.\runtime\ddns.exe start-service
```

可以检查服务当前指向的 NSSM 路径：

```powershell
sc.exe qc DDNS
```

确认服务是否已经卸载：

```powershell
sc.exe query DDNS
```

如果返回 `1060` 或 `The specified service does not exist as an installed service`，说明服务已经不存在。
