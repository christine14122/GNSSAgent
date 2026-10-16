# GNSSAgent-CCU 自启动

参考 LRS，自启动仅支持 Linux x86-64、SysV + chkconfig，依赖 util-linux 的 flock。

将本目录所有文件和 Linux amd64 程序 `GNSSAgent-CCU` 放入 `/home/TEDS/GNSSAgent`，以 root 执行：

```sh
cd /home/TEDS/GNSSAgent
sh install.sh --start
```

注册 `/etc/init.d/gnssagent`，运行级别 2、3、4、5 开机启动。不带 `--start` 时询问是否立即启动；也兼容 `sh install.sh sysv --start`。

```sh
sh start.sh
sh status.sh
sh restart.sh
sh stop.sh
# 停止并取消自启动，保留程序和日志
sh uninstall.sh
```

程序使用默认监听参数（UDP/TCP 29501），工作目录为 `/home/TEDS/GNSSAgent`。
日志为 `/home/TEDS/Logs/GNSSAgent/gnssagent.log`，使用程序自带轮转（8 MiB、一个备份）。
PID 文件记录 PID 和进程启动时间，控制操作加锁，停止前核对程序身份；15 秒内未退出则报错，不强杀。
与 LRS 一样只提供开机启动，不提供退出后自动拉起。
