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
PID 文件记录 PID、进程启动时间和本次系统启动的 `boot_id`，控制操作加锁，停止前核对程序身份；15 秒内未退出则报错，不强杀。
与 LRS 一样只提供开机启动，不提供退出后自动拉起。

## 升级注意事项

升级包只包含程序、配置和本目录部署文件。不要从运行目录整体打包：必须排除
`gnssagent.pid`、`gnssagent.pid.*`（包括锁和临时文件）以及日志。不要预先创建空 PID 文件。

先使用旧版本的 `service gnssagent stop`，确认成功后替换程序和脚本，再执行
`sh install.sh sysv --start`。停止失败时应先处理现有进程。安装完成后尝试启动失败可能只是通信参数尚未下发，
仅提示警告，不影响安装成功；补齐参数后再启动。文件部署或自启动注册失败仍须返回非零，
由总安装脚本汇总。手动执行 start/status 仍返回真实运行结果。

PID 信息通过同目录临时文件原子替换。启动时扫描实际程序：空/缺失 PID 文件、上次
系统启动留下的记录，以及可证明已失效的旧两字段记录均可恢复；若同路径只有一个
有效进程，接管该进程并补写记录，不再启动第二个实例。若没有实际进程，则正常启动。

停止和状态检查仍保持保守处理：空/缺失 PID 但实际程序仍在运行时返回非零，避免
误报已停止或给未经核验的进程发信号。非空但格式错误的记录、符号链接和多个实际
进程均拒绝自动恢复；同次启动的三字段记录身份不匹配时也要求人工核对。
安装依赖检查包含原子写入所需的 `mktemp` 和进程核验所需的 `tr`。
