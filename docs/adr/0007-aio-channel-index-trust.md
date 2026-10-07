---
status: accepted
---

# 整合包的执行信任来自通道索引

空机器安装整合包时，root 以该通道固定通道索引里的 sha256 作为唯一信任根，核对后执行包内的 Mihari；不再为了取得安装器去连接 GitHub，也不再下载第二份程序。本机已有合格安装器时仍由它执行。完全离线时不联网，只接受已安装的安装器或管理员钉在 install-trust 里的摘要。没有整合包的 `install.sh` 仍以 GitHub 发布为权威。

否决过的做法是继续用 GitHub `SHA256SUMS.txt` 或 release 列表给整合包授权。那会使已经按通道索引下完的包在安装时再次联网，离线安装也无法在没有安装器时闭环。在 Unix root 阶段，调用方传入的摘要和 `MIHARI_INDEX_URL` 不作为信任根。Windows 安装脚本不在本次范围内，仍可读 `MIHARI_INDEX_URL`。
