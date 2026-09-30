package templates

import "keyhive/internal/model"

// Group 分组名
const (
	GroupCloud    = "云平台"
	GroupRegistry = "容器镜像"
	GroupServer   = "服务器/SSH"
	GroupDB       = "数据库"
	GroupMiddleware = "中间件/服务"
	GroupDevOps   = "DevOps"
	GroupCert     = "证书/授权"
	GroupGeneric  = "通用"
)

// 字段构造辅助：t=非敏感文本 f=指定敏感位 fu=URL fm=多行
func t(key, desc string) model.Field {
	return model.Field{Key: key, Description: desc, Type: model.FieldTypeText}
}
func tm(key, desc string) model.Field {
	return model.Field{Key: key, Description: desc, Type: model.FieldTypeMultiline}
}
func f(key, desc string, secret bool) model.Field {
	return model.Field{Key: key, Description: desc, Type: model.FieldTypeText, IsSecret: secret}
}
func fu(key, desc string) model.Field {
	return model.Field{Key: key, Description: desc, Type: model.FieldTypeURL}
}
func fm(key, desc string, secret bool) model.Field {
	return model.Field{Key: key, Description: desc, Type: model.FieldTypeMultiline, IsSecret: secret}
}

// Builtin 返回全部内置模板（种子数据与前端模板选择器共用）
func Builtin() []model.Template {
	return []model.Template{
		// ---------- 云平台 ----------
		{
			Category: "aliyun_aksk", Name: "阿里云 AccessKey", Group: GroupCloud,
			Fields: []model.Field{
				f("access_key_id", "阿里云 AccessKey ID（RAM 用户的 AK），ak sk 认证时的用户名部分", true),
				f("access_key_secret", "阿里云 AccessKey Secret，与 AccessKey ID 配对使用，请妥善保密", true),
				t("account_id", "阿里云主账号 ID（数字），配置 RAM 跨账号或 STS 时使用"),
				t("region", "默认地域 ID，如 cn-hangzhou、cn-beijing"),
				t("remark", "这对密钥的用途与权限范围说明，如“仅用于 OSS 读写”"),
			},
		},
		{
			Category: "huawei_aksk", Name: "华为云 AK/SK", Group: GroupCloud,
			Fields: []model.Field{
				f("ak", "华为云 Access Key Id，CLI（hcloud）与 SDK 认证使用", true),
				f("sk", "华为云 Secret Access Key，与 AK 配对，等效于密码", true),
				t("account_name", "华为云账号名（IAM 主账号或子用户名）"),
				t("region", "默认区域，如 cn-east-3（上海一）、cn-north-4（北京四）"),
				t("remark", "用途与授权范围说明"),
			},
		},
		{
			Category: "tencent_aksk", Name: "腾讯云密钥", Group: GroupCloud,
			Fields: []model.Field{
				f("secret_id", "腾讯云 SecretId（子账号密钥 ID），API 调用身份标识", true),
				f("secret_key", "腾讯云 SecretKey，与 SecretId 配对的密钥", true),
				t("appid", "腾讯云账号 APPID（数字，可在账号信息页查看）"),
				t("region", "默认地域，如 ap-guangzhou、ap-shanghai"),
				t("remark", "用途与授权范围说明"),
			},
		},
		{
			Category: "aws_aksk", Name: "AWS IAM 密钥", Group: GroupCloud,
			Fields: []model.Field{
				f("access_key_id", "AWS Access Key ID（AKIA 开头），aws cli 认证使用", true),
				f("secret_access_key", "AWS Secret Access Key，与 Access Key ID 配对", true),
				t("region", "默认区域，如 ap-northeast-1、us-east-1"),
				t("account_id", "AWS 账号 ID（12 位数字）"),
				t("remark", "IAM 用户/角色与权限范围说明"),
			},
		},
		{
			Category: "object_storage", Name: "对象存储 OSS/S3", Group: GroupCloud,
			Fields: []model.Field{
				fu("endpoint", "对象存储 Endpoint，如 oss-cn-hangzhou.aliyuncs.com 或 s3.amazonaws.com"),
				f("access_key", "访问密钥 AK/AccessKey", true),
				f("secret_key", "访问密钥 SK/SecretKey", true),
				t("bucket", "默认 Bucket 名称"),
				t("remark", "存储用途说明，如“数据库备份桶”"),
			},
		},
		{
			Category: "k8s_cluster", Name: "Kubernetes 集群", Group: GroupCloud,
			Fields: []model.Field{
				t("name", "集群名称/别名，如 prod-cluster"),
				fu("api_server", "API Server 地址，如 https://192.168.1.10:6443"),
				fm("kubeconfig", "完整 kubeconfig YAML 内容，或 ServiceAccount token，kubectl 认证使用", true),
				t("namespace", "默认命名空间"),
				t("remark", "集群环境说明，如“生产集群，谨慎操作”"),
			},
		},

		// ---------- 容器镜像 ----------
		{
			Category: "docker_registry", Name: "Docker 镜像仓库", Group: GroupRegistry,
			Fields: []model.Field{
				t("registry", "镜像仓库地址，docker login/pull 使用，如 registry.example.com 或 docker.io"),
				t("username", "登录用户名"),
				f("password", "登录密码或 Access Token，docker login 时使用", true),
				t("namespace", "默认命名空间/仓库组"),
				t("remark", "用途说明"),
			},
		},
		{
			Category: "harbor", Name: "Harbor 私有仓库", Group: GroupRegistry,
			Fields: []model.Field{
				fu("url", "Harbor Web 控制台地址，如 https://harbor.example.com"),
				t("registry", "docker login 用的仓库地址（通常与 url 同主机）"),
				t("username", "Harbor 用户名"),
				f("password", "Harbor 密码", true),
				t("project", "默认项目名"),
			},
		},
		{
			Category: "huawei_swr", Name: "华为云 SWR", Group: GroupRegistry,
			Fields: []model.Field{
				t("registry", "SWR 拉取地址，如 swr.cn-east-3.myhuaweicloud.com，docker login/pull 使用"),
				t("org", "组织名称（命名空间），镜像存放于该组织下"),
				t("username", "登录用户名，通常为 IAM 子账号名"),
				f("password", "docker login 密码（可用华为云登录密钥）", true),
				t("region", "所在区域，如 cn-east-3"),
			},
		},

		// ---------- 服务器/SSH ----------
		{
			Category: "ssh_server", Name: "SSH 服务器", Group: GroupServer,
			Fields: []model.Field{
				t("host", "服务器 IP 或域名"),
				t("port", "SSH 端口，默认 22"),
				t("username", "登录用户名，如 root 或普通用户"),
				f("password", "登录密码（若使用密钥认证可留空）", true),
				fm("private_key", "SSH 私钥内容（若使用密码认证可留空）", true),
				f("sudo_password", "sudo 提权密码（与登录密码不同时填写）", true),
				t("remark", "这台机器的用途说明，如“生产 Nginx 节点”"),
			},
		},
		{
			Category: "ssh_key", Name: "SSH 密钥对", Group: GroupServer,
			Fields: []model.Field{
				t("name", "密钥用途别名，如 git-signing-key"),
				fm("private_key", "私钥内容（OpenSSH PEM 格式）， ssh -i 使用", true),
				tm("public_key", "公钥内容（.pub 文件，可分发到目标机器 authorized_keys）"),
				f("passphrase", "私钥口令（未设置可留空）", true),
				t("remark", "绑定了哪些服务器/代码托管平台"),
			},
		},
		{
			Category: "server_panel", Name: "服务器管理面板", Group: GroupServer,
			Fields: []model.Field{
				t("type", "面板类型：ESXi / Proxmox / 群晖 DSM / 宝塔 / 1Panel 等"),
				fu("url", "管理入口地址，如 https://192.168.1.10:8006"),
				t("username", "登录用户名"),
				f("password", "登录密码", true),
				t("remark", "管理范围与注意事项，如“虚拟化宿主机，关机影响全部 VM”"),
			},
		},
		{
			Category: "vpn", Name: "VPN/远程接入", Group: GroupServer,
			Fields: []model.Field{
				t("type", "VPN 类型：WireGuard / OpenVPN / L2TP / IPSec 等"),
				t("server", "VPN 服务器地址"),
				t("username", "账号（部分协议可留空）"),
				f("password", "密码", true),
				f("preshared_key", "预共享密钥 PSK（L2TP/IPSec 等协议使用）", true),
				t("remark", "接入哪个网络、用途说明"),
			},
		},

		// ---------- 数据库 ----------
		{
			Category: "mysql", Name: "MySQL/MariaDB", Group: GroupDB,
			Fields: []model.Field{
				t("host", "数据库地址（IP 或域名）"),
				t("port", "端口，默认 3306"),
				t("database", "数据库名"),
				t("username", "用户名"),
				f("password", "密码", true),
				t("remark", "这套库跑什么业务"),
			},
		},
		{
			Category: "postgresql", Name: "PostgreSQL", Group: GroupDB,
			Fields: []model.Field{
				t("host", "数据库地址"),
				t("port", "端口，默认 5432"),
				t("database", "数据库名"),
				t("username", "用户名"),
				f("password", "密码", true),
				t("remark", "业务说明"),
			},
		},
		{
			Category: "redis", Name: "Redis", Group: GroupDB,
			Fields: []model.Field{
				t("host", "Redis 地址"),
				t("port", "端口，默认 6379"),
				f("password", "访问密码（requirepass）", true),
				t("db", "默认 DB 编号，0-15"),
				t("tls", "是否 TLS 连接：yes/no"),
				t("remark", "用途说明"),
			},
		},
		{
			Category: "mongodb", Name: "MongoDB", Group: GroupDB,
			Fields: []model.Field{
				t("uri", "连接串，如 mongodb://user:pass@host:27017（或分开填下面字段）"),
				t("host", "地址（若不用 URI 方式）"),
				t("port", "端口，默认 27017"),
				t("username", "用户名"),
				f("password", "密码", true),
				t("auth_db", "认证库，默认 admin"),
			},
		},
		{
			Category: "mssql", Name: "SQL Server", Group: GroupDB,
			Fields: []model.Field{
				t("host", "数据库地址"),
				t("port", "端口，默认 1433"),
				t("instance", "实例名（命名实例时填写）"),
				t("database", "数据库名"),
				t("username", "用户名，通常为 sa"),
				f("password", "密码", true),
			},
		},

		// ---------- 中间件/服务 ----------
		{
			Category: "mq_service", Name: "消息队列", Group: GroupMiddleware,
			Fields: []model.Field{
				t("type", "类型：Kafka / RabbitMQ / RocketMQ / EMQX 等"),
				t("brokers", "地址列表，如 10.0.0.1:9092,10.0.0.2:9092 或 amqp://host:5672"),
				t("username", "用户名"),
				f("password", "密码", true),
				t("remark", "队列/vhost/topic 说明"),
			},
		},
		{
			Category: "smtp", Name: "SMTP 邮件", Group: GroupMiddleware,
			Fields: []model.Field{
				t("server", "SMTP 服务器，如 smtp.qq.com、smtp.163.com"),
				t("port", "端口：465（SSL）/ 587（STARTTLS）/ 25"),
				t("encryption", "加密方式：SSL / STARTTLS / 无"),
				t("username", "邮箱账号"),
				f("password", "密码或授权码（QQ/163 邮箱为授权码，非登录密码）", true),
				t("from", "发件人地址，通常与账号相同"),
				t("remark", "用途，如“监控告警发件箱”"),
			},
		},
		{
			Category: "alert_webhook", Name: "告警 Webhook", Group: GroupMiddleware,
			Fields: []model.Field{
				t("platform", "平台：钉钉 / 企业微信 / 飞书 / Slack"),
				f("webhook_url", "机器人 Webhook 地址，POST 消息到此 URL", true),
				f("secret", "加签密钥（钉钉机器人 SEC 开头；未开启加签可留空）", true),
				t("remark", "这个机器人接到哪个告警源"),
			},
		},
		{
			Category: "network_proxy", Name: "网络代理", Group: GroupMiddleware,
			Fields: []model.Field{
				t("protocol", "协议：http / https / socks5"),
				t("address", "代理地址与端口，如 10.0.0.1:7890"),
				t("username", "用户名（无认证可留空）"),
				f("password", "密码", true),
				t("no_proxy", "不走代理的地址列表，逗号分隔，如 localhost,192.168.0.0/22"),
				t("remark", "代理出口用途说明"),
			},
		},

		// ---------- DevOps ----------
		{
			Category: "git_service", Name: "Git 托管平台", Group: GroupDevOps,
			Fields: []model.Field{
				t("platform", "平台：GitHub / Gitee / GitLab / Gitea"),
				t("username", "用户名"),
				f("token", "Personal Access Token，git https 拉取与 API 调用使用", true),
				fm("ssh_key", "账号绑定的 SSH 私钥（若用 SSH 方式拉代码）", true),
				t("remark", "权限范围，如“只读公开仓库”"),
			},
		},
		{
			Category: "jenkins_ci", Name: "Jenkins/CI 平台", Group: GroupDevOps,
			Fields: []model.Field{
				fu("url", "平台地址，如 https://ci.example.com"),
				t("username", "用户名"),
				f("api_token", "API Token（在用户设置中生成），用于 REST API 与 CLI", true),
				t("remark", "权限与用途说明"),
			},
		},
		{
			Category: "monitoring", Name: "监控平台", Group: GroupDevOps,
			Fields: []model.Field{
				t("type", "类型：Grafana / Zabbix / Prometheus / 夜莺等"),
				fu("url", "平台地址"),
				t("username", "用户名"),
				f("password", "密码", true),
				f("api_key", "API Key / Service Account Token（Grafana 等）", true),
				t("remark", "监控范围说明"),
			},
		},
		{
			Category: "dns_provider", Name: "DNS 服务商", Group: GroupDevOps,
			Fields: []model.Field{
				t("provider", "服务商：Cloudflare / 阿里云 DNS / DNSPod / 腾讯云"),
				f("api_key", "API Key / AccessKey（certbot、ddns 等工具使用）", true),
				f("api_secret", "API Secret / SecretKey", true),
				t("zone", "托管的域名，如 example.com"),
				t("remark", "用途，如“泛域名证书自动续期”"),
			},
		},

		// ---------- 证书/授权 ----------
		{
			Category: "ssl_cert", Name: "SSL 证书", Group: GroupCert,
			Fields: []model.Field{
				t("domain", "证书覆盖的域名，泛域名如 *.example.com"),
				t("issuer", "颁发机构：Let's Encrypt / DigiCert / 阿里云等"),
				t("expires", "到期日期 YYYY-MM-DD"),
				tm("cert_pem", "证书全文 PEM 格式（含中间链）"),
				fm("key_pem", "私钥全文 PEM 格式，nginx/tomcat 部署使用", true),
				t("remark", "部署在哪些机器上"),
			},
		},
		{
			Category: "software_license", Name: "软件许可证", Group: GroupCert,
			Fields: []model.Field{
				t("software", "软件名称与版本，如 JetBrains IDEA 2026"),
				f("license_key", "许可证密钥/激活码", true),
				t("account", "绑定的账号邮箱"),
				t("expires", "到期日期或“永久”"),
				t("remark", "授权台数、转让限制等"),
			},
		},

		// ---------- 通用 ----------
		{
			Category: "web_account", Name: "网站/应用账号", Group: GroupGeneric,
			Fields: []model.Field{
				fu("url", "网站或应用地址"),
				t("username", "用户名/邮箱/手机号"),
				f("password", "密码", true),
				f("totp_secret", "两步验证 TOTP 密钥（扫码二维码里的 secret，用于生成动态码）", true),
				t("recovery_email", "找回邮箱"),
				t("remark", "账号用途备注"),
			},
		},
		{
			Category: "api_key", Name: "通用 API Key", Group: GroupGeneric,
			Fields: []model.Field{
				t("service", "服务名称，如“OpenAI API”"),
				f("api_key", "API Key 主密钥", true),
				f("api_secret", "API Secret（成对密钥体系时填写）", true),
				fu("endpoint", "API 调用地址"),
				fu("docs", "API 文档地址"),
				t("remark", "额度、用途说明"),
			},
		},
		{
			Category: "blank", Name: "空白模板（自由定义）", Group: GroupGeneric,
			Fields: []model.Field{
				f("field1", "字段说明：写给 AI 看的用途描述", false),
			},
		},
	}
}
