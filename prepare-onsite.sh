#!/usr/bin/env bash
# prepare-onsite.sh —— 工程师侧预置工具（在家/办公机运行）。
#
# 交互式选好“要应用哪些功能、每个功能作用于哪些腔室”，然后生成一个
# 自包含的现场一键执行脚本（updateConfig-<日期>.sh）。生成的脚本：
#   * 只给现场操作人员两个选项：1) 应用升级配置  2) 退出；
#   * 把工程师选定的功能/腔室/配置目录写死，现场不能再改，避免选错；
#   * 每次执行自动在 logs/ 下生成完整日志（环境、预检、每条命令、退出码、
#     配置变更清单）；默认不自动备份 config（现场人员会手工备份），需要时
#     现场加 --backup 可由脚本备份，便于问题排查与回滚。
#
# 用法：bash prepare-onsite.sh [--config <目录>] [-o|--output <文件>]
#
# 约定与 run.sh 一致：features/ 以本脚本所在目录为基准解析；生成的脚本
# 须与 auto-config-update-32、features/、config/ 放在现场同一目录。
if [ -z "${BASH_VERSION:-}" ]; then
    exec bash "$0" "$@"
fi
set -u

base_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P) || exit 1
features_dir=$base_dir/features
runner=$base_dir/auto-config-update-32
chambers=(Ch1 Ch2 Ch3 Ch4 Ch5 Ch6 ChA ChB ChC ChD ChE ChF)

usage() {
    printf '用法: %s [--config <目录>] [-o|--output <文件>]\n' "$0"
    printf '  --config <目录>      现场 config 目录（相对路径以可执行文件目录为基准；缺省 config）\n'
    printf '  -o, --output <文件>  生成的现场脚本路径（缺省 <本脚本目录>/updateConfig-<日期>.sh）\n'
    printf '  -h, --help           显示本帮助\n'
}

config_spec=''
output=''
while (($#)); do
    case $1 in
        --config)
            if (($# < 2)) || [[ -z $2 ]]; then
                printf '错误：--config 需要一个目录参数。\n' >&2
                usage >&2
                exit 2
            fi
            config_spec=$2
            shift 2
            ;;
        -o|--output)
            if (($# < 2)) || [[ -z $2 ]]; then
                printf '错误：%s 需要一个文件参数。\n' "$1" >&2
                usage >&2
                exit 2
            fi
            output=$2
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            printf '错误：未知参数 %s\n' "$1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ ! -d $features_dir ]]; then
    printf '缺少 features/ 目录：%s\n' "$features_dir" >&2
    exit 1
fi
if [[ ! -x $runner ]]; then
    printf '提示：未找到可执行文件 %s；生成的现场脚本仍要求它与脚本同目录。\n' "$runner" >&2
fi

# 读取顶层单行 YAML 标量；不尝试用正则解析 steps 等复杂 YAML 结构。
# 与 run.sh 保持一致：id/description 缺失或使用多行标量时拒绝生成，
# 避免把不完整的功能列给工程师。
read_metadata() {
    awk '
        /^(id|description):[[:space:]]*/ {
            key = $0
            sub(/:.*/, "", key)
            value = $0
            sub(/^[^:]*:[[:space:]]*/, "", value)
            sub(/[[:space:]]+$/, "", value)
            if (value ~ /^[|>]/) { invalid = 1; next }
            if (value ~ /^".*"$/ || value ~ /^\047.*\047$/) value = substr(value, 2, length(value) - 2)
            if (key == "id") id = value
            else description = value
        }
        END {
            if (invalid || id == "" || description == "") exit 1
            printf "%s\n%s\n", id, description
        }
    ' "$1"
}

paths=() labels=() descriptions=() selected=() chamber_choices=()
while IFS= read -r -d '' path; do
    if ! metadata=$(read_metadata "$path"); then
        printf '无法读取功能的 id/description（须为顶层单行字段）：%s\n' "$path" >&2
        exit 1
    fi
    id=${metadata%%$'\n'*}
    description=${metadata#*$'\n'}
    if [[ $id == *$'\t'* || $description == *$'\t'* || $description == *$'\n'* ]]; then
        printf '功能元数据不能包含制表符或换行：%s\n' "$path" >&2
        exit 1
    fi
    relative=${path#"$features_dir"/}
    folder=${relative%/*}
    if [[ $folder == "$relative" ]]; then
        label=$id
    else
        label=$folder/$id
    fi
    paths+=("$path")
    labels+=("$label")
    descriptions+=("$description")
    selected+=(0)
    chamber_choices+=("")
done < <(find "$features_dir" -type f \( -name '*.yaml' -o -name '*.yml' \) -print0 | sort -z)

if ((${#paths[@]} == 0)); then
    printf 'features/ 中没有 YAML 文件。\n' >&2
    exit 1
fi

# 只在交互终端刷新；重定向输出或用管道输入时保留完整过程。
refresh_screen() {
    if [[ -t 0 && -t 1 && ${TERM:-dumb} != dumb ]]; then
        printf '\033[H\033[2J'
    fi
}
# 输入必须是纯十进制整数，不能把带前导零的输入当成八进制。
parse_index() {
    [[ $1 =~ ^[0-9]+$ ]] || return 1
    number=$((10#$1))
    ((number >= 1 && number <= $2))
}

choose_chambers() {
    local feature=$1 input number i mark choices
    local -a picked=()
    for ((i=0; i<${#chambers[@]}; i++)); do
        if [[ " ${chamber_choices[feature]} " == *" ${chambers[i]} "* ]]; then
            picked[i]=1
        else
            picked[i]=0
        fi
    done
    while :; do
        refresh_screen
        printf '\n请选择要应用 %s 功能的腔室（均不选 = 全部腔室）：\n' "${labels[feature]}"
        for ((i=0; i<${#chambers[@]}; i++)); do
            mark=' '
            ((picked[i])) && mark=x
            printf '%2d. [%s] %s\n' "$((i+1))" "$mark" "${chambers[i]}"
        done
        printf '输入编号切换，回车保存并返回：'
        if ! IFS= read -r input; then
            printf '\n输入已结束，取消生成。\n' >&2
            exit 1
        fi
        if [[ -z $input ]]; then
            choices=''
            for ((i=0; i<${#chambers[@]}; i++)); do
                ((picked[i])) && choices+=" ${chambers[i]}"
            done
            chamber_choices[feature]=${choices# }
            return
        fi
        if parse_index "$input" "${#chambers[@]}"; then
            i=$((number-1))
            picked[i]=$((1-picked[i]))
        else
            printf '无效编号：%s\n' "$input" >&2
        fi
    done
}

while :; do
    refresh_screen
    printf '\n===== 预置现场一键执行方案 =====\n'
    printf '功能列表（输入编号勾选/取消；勾选后进入腔室选择）：\n'
    for ((i=0; i<${#paths[@]}; i++)); do
        mark=' '
        ((selected[i])) && mark=x
        printf '%2d. [%s] %s: %s' "$((i+1))" "$mark" "${labels[i]}" "${descriptions[i]}"
        if ((selected[i])); then
            printf '（腔室：%s）' "${chamber_choices[i]:-全部}"
        fi
        printf '\n'
    done
    printf '输入编号、g 生成脚本、q 退出：'
    if ! IFS= read -r input; then
        printf '\n输入已结束，取消生成。\n' >&2
        exit 1
    fi
    case $input in
        q|Q)
            printf '已取消，未生成脚本。\n'
            exit 0
            ;;
        g|G)
            count=0
            for value in "${selected[@]}"; do ((value)) && ((count+=1)); done
            if ((count == 0)); then
                printf '请至少选择一个功能。\n' >&2
                continue
            fi
            break
            ;;
        *)
            if parse_index "$input" "${#paths[@]}"; then
                i=$((number-1))
                if ((selected[i])); then
                    selected[i]=0
                    chamber_choices[i]=''
                else
                    selected[i]=1
                    choose_chambers "$i"
                fi
            else
                printf '无效输入：%s\n' "$input" >&2
            fi
            ;;
    esac
done

# 收集生成信息，写入日志头部，便于现场问题追溯。
read_value() {
    printf '%s' "$1"
    if ! IFS= read -r value; then
        printf '\n输入已结束，取消生成。\n' >&2
        exit 1
    fi
}
generated_at=$(date '+%Y-%m-%d %H:%M:%S %z')
stamp=$(date '+%Y%m%d')
generated_user=${USER:-$(id -un 2>/dev/null || echo unknown)}
generated_host=$(hostname 2>/dev/null || echo unknown)

printf '\n'
read_value "生成人（回车取当前用户 ${generated_user}）："
engineer=${value:-$generated_user}
read_value '工位/设备标识（可选，回车跳过）：'
site=$value
read_value '备注（可选，回车跳过）：'
note=$value

if [[ -z $output ]]; then
    output=$base_dir/updateConfig-$stamp.sh
    suffix=1
    while [[ -e $output ]]; do
        output=$base_dir/updateConfig-$stamp-$suffix.sh
        suffix=$((suffix + 1))
    done
elif [[ -e $output ]]; then
    printf '输出文件已存在，拒绝覆盖：%s\n' "$output" >&2
    exit 1
fi

# 校验选中的功能定义可读，并计算 sha256 供现场核对版本。
sha256_of() {
    local out
    if command -v sha256sum >/dev/null 2>&1; then
        out=$(sha256sum -- "$1" 2>/dev/null) || return 1
    elif command -v shasum >/dev/null 2>&1; then
        out=$(shasum -a 256 -- "$1" 2>/dev/null) || return 1
    else
        return 1
    fi
    printf '%s' "${out%% *}"
}

out_features=() out_labels=() out_chambers=() out_hashes=()
for ((i=0; i<${#paths[@]}; i++)); do
    ((selected[i])) || continue
    relative=${paths[i]#"$features_dir"/}
    feature_rel=features/$relative
    if ! hash=$(sha256_of "${paths[i]}"); then
        printf '无法计算功能定义 sha256（缺少 sha256sum/shasum）：%s\n' "${paths[i]}" >&2
        exit 1
    fi
    out_features+=("$feature_rel")
    out_labels+=("${labels[i]}: ${descriptions[i]}")
    out_chambers+=("${chamber_choices[i]}")
    out_hashes+=("$hash")
done

if ((${#out_features[@]} == 0)); then
    printf '没有可生成的功能。\n' >&2
    exit 1
fi

plan_text=$(
    {
        printf 'format=prepare-onsite/1\n'
        printf 'created=%s\n' "$generated_at"
        printf 'engineer=%s\n' "$engineer"
        printf 'site=%s\n' "$site"
        printf 'note=%s\n' "$note"
        printf 'config=%s\n' "$config_spec"
        for ((i=0; i<${#out_features[@]}; i++)); do
            printf '%s|%s|%s\n' "${out_features[i]}" "${out_hashes[i]}" "${out_chambers[i]}"
        done
    }
)
if command -v sha256sum >/dev/null 2>&1; then
    plan_fingerprint=$(printf '%s\n' "$plan_text" | sha256sum | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
    plan_fingerprint=$(printf '%s\n' "$plan_text" | shasum -a 256 | cut -d' ' -f1)
else
    printf '无法计算方案指纹（缺少 sha256sum/shasum）。\n' >&2
    exit 1
fi

printf '\n===== 方案预览 =====\n'
printf '将按以下顺序执行（共 %d 项功能）：\n' "${#out_features[@]}"
for ((i=0; i<${#out_features[@]}; i++)); do
    printf '  %d. %s  ->  %s\n' "$((i+1))" "${out_labels[i]}" "${out_chambers[i]:-全部腔室}"
done
printf '配置目录：%s\n' "${config_spec:-config（缺省）}"
printf '生成人：%s\n' "$engineer"
printf '工位/设备：%s\n' "${site:-（未填写）}"
printf '备注：%s\n' "${note:-（无）}"
printf '方案指纹：%s\n' "$plan_fingerprint"
printf '输出文件：%s\n' "$output"
printf '确认生成？输入 y 继续，其他取消：'
if ! IFS= read -r input || [[ $input != y && $input != Y ]]; then
    printf '\n已取消，未生成脚本。\n'
    exit 0
fi

# 单引号包裹并把内部的 ' 转义成 '\''；不依赖 printf %q（Bash 4.1 的 %q 不转义开头 ~）。
quote_value() {
    local value=$1
    value=${value//\'/\'\\\'\'}
    printf "'%s'" "$value"
}
emit_var() {
    printf '%s=' "$1"
    quote_value "$2"
    printf '\n'
}
emit_array() {
    local name=$1 item
    shift
    printf '%s=(' "$name"
    for item in "$@"; do
        quote_value "$item"
        printf ' '
    done
    printf ')\n'
}

output_dir=$(dirname -- "$output")
if ! mkdir -p -- "$output_dir"; then
    printf '无法创建输出目录：%s\n' "$output_dir" >&2
    exit 1
fi
tmp_output=$output.tmp.$$
if ! : >"$tmp_output" 2>/dev/null; then
    printf '无法写入输出文件：%s\n' "$output" >&2
    exit 1
fi

{
    cat <<'GEN_HEADER'
#!/usr/bin/env bash
# ============================================================
# 现场一键执行脚本（由 prepare-onsite.sh 自动生成，请勿手工修改）
#
# 放置：与 auto-config-update-32、features/、config/ 放在同一目录。
# 用法：bash <本脚本> [--yes] [--force] [--backup] [--log-dir <目录>]
#   --yes        不显示菜单，直接执行（工程师远程指导时可用）
#   --force      功能定义校验不一致时仍继续（危险，仅工程师明确原因时使用）
#   --backup     执行前自动备份 config（缺省不备份；现场人员会手工备份）
#   --log-dir    指定日志目录（缺省 <脚本目录>/logs）
#
# 每次执行都会生成独立日志；是否自动备份由 --backup 决定。
# 需要调整功能/腔室时，请工程师重新运行 prepare-onsite.sh 生成新脚本。
# ============================================================
if [ -z "${BASH_VERSION:-}" ]; then
    exec bash "$0" "$@"
fi
set -u

GEN_HEADER
    emit_var GEN_AT "$generated_at"
    emit_var GEN_USER "$engineer"
    emit_var GEN_HOST "$generated_host"
    emit_var SITE_NAME "$site"
    emit_var SITE_NOTE "$note"
    emit_var PLAN_CONFIG "$config_spec"
    emit_var PLAN_FINGERPRINT "$plan_fingerprint"
    emit_array PLAN_LABELS "${out_labels[@]}"
    emit_array PLAN_FEATURES "${out_features[@]}"
    emit_array PLAN_CHAMBERS "${out_chambers[@]}"
    emit_array PLAN_HASHES "${out_hashes[@]}"
    printf 'BACKUP_ENABLED=0\n'
    printf '\n'
    cat <<'GEN_BODY'
script_file=${BASH_SOURCE[0]}
script_dir=$(cd -- "$(dirname -- "$script_file")" && pwd -P) || exit 1
script_path=$script_dir/$(basename -- "$script_file")
base_dir=$script_dir
runner=$base_dir/auto-config-update-32
cd -- "$base_dir" || exit 1

invocation_args=${*:-（无）}
auto_yes=0
force=0
backup_enabled=$BACKUP_ENABLED
log_dir_option=''
log_dir=''
log_file=''
work_dir=''
config_abs=''
backup_file=''
before_snapshot=''
after_snapshot=''
snapshot_ready=0
config_arg=''
lock_file=$base_dir/.updateConfig.lock
lock_held=0
forced_hash_mismatch=0
total=0
current_step=0
current_running=0

usage() {
    printf '用法：bash %s [--yes] [--force] [--backup] [--log-dir <目录>]\n' "$(basename -- "$script_file")"
    printf '  --yes            不显示菜单，直接执行\n'
    printf '  --force          功能定义与预置方案不一致时仍继续（仅工程师使用）\n'
    printf '  --backup         执行前自动备份 config（缺省不备份）\n'
    printf '  --log-dir <目录> 指定日志目录（缺省 <脚本目录>/logs）\n'
    printf '  -h, --help       显示本帮助\n'
}

while (($#)); do
    case $1 in
        --yes)
            auto_yes=1
            shift
            ;;
        --force)
            force=1
            shift
            ;;
        --backup)
            backup_enabled=1
            shift
            ;;
        --log-dir)
            if (($# < 2)) || [[ -z $2 ]]; then
                printf '错误：--log-dir 需要一个目录参数。\n' >&2
                exit 2
            fi
            log_dir_option=$2
            shift 2
            ;;
        --log-dir=*)
            log_dir_option=${1#*=}
            if [[ -z $log_dir_option ]]; then
                printf '错误：--log-dir 不能为空。\n' >&2
                exit 2
            fi
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            printf '错误：未知参数 %s\n' "$1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

# 同时输出到屏幕和日志；日志是本脚本排查问题的唯一凭据，必须始终可写。
say() {
    printf '%s\n' "$*"
    printf '%s\n' "$*" >>"$log_file"
}
log_only() {
    printf '%s\n' "$*" >>"$log_file"
}
ask() {
    printf '%s' "$1"
    printf '%s' "$1" >>"$log_file"
    if IFS= read -r reply; then
        printf '%s\n' "$reply" >>"$log_file"
        return 0
    fi
    printf '\n'
    printf '[输入结束]\n' >>"$log_file"
    return 1
}

setup_logging() {
    local fallback_note='' note_prefix=''
    if [[ -n $log_dir_option ]]; then
        log_dir=$log_dir_option
        note_prefix='! 指定的日志目录不可写'
    else
        log_dir=$base_dir/logs
        note_prefix='提示：脚本目录不可写'
    fi
    if ! mkdir -p -- "$log_dir" 2>/dev/null || [[ ! -w $log_dir ]]; then
        log_dir=${TMPDIR:-/tmp}/auto-upgrade-logs
        if ! mkdir -p -- "$log_dir" 2>/dev/null || [[ ! -w $log_dir ]]; then
            printf '错误：无法创建日志目录（已尝试 %s 与 %s），已停止。\n' \
                "${log_dir_option:-$base_dir/logs}" "$log_dir" >&2
            exit 4
        fi
        fallback_note="$note_prefix，日志已改写到：$log_dir"
    fi
    log_dir=$(cd -- "$log_dir" && pwd -P) || exit 4
    log_file=$log_dir/upgrade-$(date '+%Y%m%d-%H%M%S')-$$.log
    if ! : >"$log_file" 2>/dev/null; then
        printf '错误：无法写入日志文件 %s，已停止。\n' "$log_file" >&2
        exit 4
    fi
    chmod 600 -- "$log_file" 2>/dev/null || true
    if [[ -n $fallback_note ]]; then
        printf '%s\n' "$fallback_note"
        printf '%s\n' "$fallback_note" >>"$log_file"
    fi
}

sha256_of() {
    local out
    if command -v sha256sum >/dev/null 2>&1; then
        out=$(sha256sum -- "$1" 2>/dev/null) || return 1
    elif command -v shasum >/dev/null 2>&1; then
        out=$(shasum -a 256 -- "$1" 2>/dev/null) || return 1
    else
        return 1
    fi
    printf '%s' "${out%% *}"
}

show_plan() {
    local i
    say "将执行的功能（按以下顺序，共 ${#PLAN_FEATURES[@]} 项）："
    for ((i=0; i<${#PLAN_FEATURES[@]}; i++)); do
        if [[ -n ${PLAN_CHAMBERS[i]} ]]; then
            say "  $((i+1)). ${PLAN_LABELS[i]}  ->  腔室：${PLAN_CHAMBERS[i]}"
        else
            say "  $((i+1)). ${PLAN_LABELS[i]}  ->  全部腔室"
        fi
    done
    say "配置目录：${PLAN_CONFIG:-config（缺省）}"
}

write_log_header() {
    local i
    say '================================================================'
    say ' 自动升级执行日志'
    say '================================================================'
    say "脚本：$script_path"
    say "生成时间：$GEN_AT"
    say "生成人：$GEN_USER（生成主机：$GEN_HOST）"
    say "工位/设备：${SITE_NAME:-（未填写）}"
    say "备注：${SITE_NOTE:-（无）}"
    say "方案指纹：$PLAN_FINGERPRINT"
    say "执行时间：$(date '+%Y-%m-%d %H:%M:%S %z')"
    say "主机名：$(hostname 2>/dev/null || echo 未知)"
    say "当前用户：$(id -un 2>/dev/null || echo 未知)"
    say "系统：$(uname -srm 2>/dev/null || echo 未知)"
    say "Bash：${BASH_VERSION}"
    say "工作目录：$base_dir"
    say "磁盘空间：$(df -h "$base_dir" 2>/dev/null | tail -n 1)"
    say "日志：$log_file"
    say "运行参数：$invocation_args"
    say ''
    say '── 预置方案 ──'
    show_plan
    for ((i=0; i<${#PLAN_FEATURES[@]}; i++)); do
        log_only "  文件：${PLAN_FEATURES[i]}  sha256=${PLAN_HASHES[i]}"
    done
    say ''
}

show_menu() {
    say ''
    say '===== 自动升级脚本 ====='
    say '    1) 应用升级配置'
    say '    2) 退出'
    say '========================'
    say ''
    say "预置方案：${SITE_NAME:-（未填写工位）}"
    show_plan
    say '备份：默认不自动备份 config，执行前请确认已手工备份（需要自动备份时加 --backup）。'
    say ''
}

choose_action() {
    while :; do
        show_menu
        if ! ask '请输入 1 或 2：'; then
            say ''
            say '[退出] 未收到输入，未做任何修改。'
            exit 1
        fi
        case $reply in
            1)
                say ''
                say '已选择：应用升级配置'
                return 0
                ;;
            2)
                say ''
                say '已退出，未做任何修改。'
                exit 0
                ;;
            *)
                say "无效输入：$reply（应输入 1 或 2）。"
                ;;
        esac
    done
}

preflight() {
    local i feature_abs actual expected check_out check_status file_count mismatched=0
    local runner_real runner_dir
    say '── 预检 ──'
    if [[ ! -x $runner ]]; then
        say "!! 找不到可执行文件或不可执行：$runner"
        return 1
    fi
    say "二进制：$runner"
    # Go 的 os.Executable() 会解析软链，--config 相对路径以“二进制真实目录”为基准。
    # 这里先解析 runner 真身；后续 check/apply 一律传绝对 --config，保证预检、备份、
    # 实际写盘三者看的是同一个 config（避免二进制被软链到别处时各看各的目录）。
    if command -v readlink >/dev/null 2>&1; then
        runner_real=$(readlink -f -- "$runner" 2>/dev/null) || runner_real=$runner
    else
        runner_real=$runner
    fi
    runner_dir=$(dirname -- "$runner_real")
    if command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1; then
        say "  sha256：$(sha256_of "$runner" || echo 无法计算)"
    fi
    say "  大小：$(stat -c %s "$runner" 2>/dev/null || echo 未知) 字节"

    if [[ -z $PLAN_CONFIG ]]; then
        config_abs=$runner_dir/config
    elif [[ $PLAN_CONFIG = /* ]]; then
        config_abs=$PLAN_CONFIG
    else
        config_abs=$runner_dir/$PLAN_CONFIG
    fi
    if [[ ! -d $config_abs ]]; then
        say "!! 配置目录不存在：$config_abs"
        return 1
    fi
    config_abs=$(cd -- "$config_abs" && pwd -P) || { say "!! 配置目录不可访问：$config_abs"; return 1; }
    config_arg=$config_abs
    say "配置目录：$config_abs"
    if [[ ! -w $config_abs ]]; then
        say "!! 配置目录不可写：$config_abs（请用 root 或调整权限）"
        return 1
    fi
    file_count=$(find "$config_abs" -type f 2>/dev/null | wc -l | tr -d ' ')
    if [[ $file_count == 0 ]]; then
        say "!! 配置目录为空：$config_abs"
        return 1
    fi
    say "  文件数：$file_count"

    if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
        say '! 系统缺少 sha256sum/shasum，跳过功能定义校验（请工程师人工确认包版本）。'
    else
        for ((i=0; i<${#PLAN_FEATURES[@]}; i++)); do
            feature_abs=$base_dir/${PLAN_FEATURES[i]}
            if [[ ! -f $feature_abs ]]; then
                say "!! 缺少功能定义：$feature_abs"
                return 1
            fi
            actual=$(sha256_of "$feature_abs" || echo 无法计算)
            expected=${PLAN_HASHES[i]}
            if [[ $actual != "$expected" ]]; then
                say "!! 功能定义与预置方案不一致：${PLAN_FEATURES[i]}"
                say "   预置 sha256：$expected"
                say "   实际 sha256：$actual"
                mismatched=$((mismatched + 1))
                if ((force)); then
                    say '! 已指定 --force，继续执行（风险自负）。'
                    forced_hash_mismatch=1
                else
                    say '   请联系工程师重新生成一键脚本，不要继续执行。'
                    return 1
                fi
            fi
        done
        if ((mismatched > 0)); then
            say "功能定义校验：$mismatched 项不一致（已按 --force 忽略）"
        else
            say '功能定义校验：通过'
        fi
    fi

    say ''
    say "Setup 一致性预检：auto-config-update-32 check（只读，不修改配置）"
    check_args=(check --config "$config_arg")
    check_out=$("$runner" "${check_args[@]}" 2>&1)
    check_status=$?
    if [[ -n $check_out ]]; then
        say "$check_out"
    fi
    say "  退出码：$check_status"
    if ((check_status != 0 && check_status != 1)); then
        say "!! 无法正常运行 auto-config-update-32（退出码 $check_status）；请核对二进制是否与现场系统匹配。"
        return 1
    fi
    if ((check_status == 1)); then
        say '! Setup 自检有告警/错误（可能升级前已存在）；apply 结束会再次校验，请关注执行结果。'
    fi
    say '预检通过。'
    return 0
}

backup_config() {
    local parent name size
    backup_file=''
    if ((backup_enabled == 0)); then
        say '配置备份：缺省不备份（现场人员手工备份）；需要时现场加 --backup。'
        return 0
    fi
    parent=$(dirname -- "$config_abs")
    name=$(basename -- "$config_abs")
    backup_file=$log_dir/config-backup-$(date '+%Y%m%d-%H%M%S')-$$.tar.gz
    if tar -czf "$backup_file" -C "$parent" -- "$name" 2>>"$log_file"; then
        chmod 600 -- "$backup_file" 2>/dev/null || true
        size=$(stat -c %s "$backup_file" 2>/dev/null || echo 未知)
        say "配置备份：$backup_file（$size 字节）"
        log_only "  回滚（工程师）：rm -rf \"$config_abs\" && tar -xzf \"$backup_file\" -C \"$parent\""
        return 0
    fi
    rm -f -- "$backup_file" 2>/dev/null
    backup_file=''
    say '!! 配置备份失败。'
    return 1
}

# 记录 config 下每个文件的 sha256，供执行前后对比；文件名可含空格，用 TAB 分隔。
snapshot_config() {
    local out=$1
    if ! command -v sha256sum >/dev/null 2>&1; then
        return 1
    fi
    # 子 shell 内开 pipefail：find/xargs 读不到文件时返回非零，避免“没读到却报告无变更”。
    (
        set -o pipefail
        find "$config_abs" -type f -print0 2>/dev/null \
            | sort -z \
            | xargs -0 -r sha256sum 2>/dev/null \
            | sed 's/^\([0-9a-f]\{64\}\)  /\1\t/' >"$out"
    ) 2>/dev/null
}

report_changes() {
    local before=$1 after=$2 list
    list=$(awk -F'\t' '
        NR == FNR { old[$2] = $1; next }
        { new[$2] = $1 }
        END {
            for (f in new) {
                if (!(f in old)) print "  新增：" f
                else if (old[f] != new[f]) print "  修改：" f
            }
            for (f in old) if (!(f in new)) print "  删除：" f
        }
    ' "$before" "$after" | sort)
    if [[ -z $list ]]; then
        say '配置变更：无（全部已是目标状态）。'
    else
        say '配置变更：'
        say "$list"
    fi
}

# 简单互斥：同一交付目录同时只允许一个升级进程，避免两个 apply 交错写盘。
acquire_lock() {
    local owner=''
    if [[ ! -w $base_dir ]]; then
        say "! 脚本目录不可写，无法创建升级锁：$lock_file（请勿并行执行升级）"
        return 0
    fi
    if ( set -C; : >"$lock_file" ) 2>/dev/null; then
        printf '%s\n' "$$" >"$lock_file"
        lock_held=1
        return 0
    fi
    if [[ -f $lock_file ]]; then
        owner=$(cat -- "$lock_file" 2>/dev/null)
    fi
    if [[ -n $owner ]] && kill -0 "$owner" 2>/dev/null; then
        say "!! 已有升级进程在执行（pid=$owner，锁：$lock_file）。"
        say '   请等待其结束后再运行，不要并行执行升级。'
        return 1
    fi
    if [[ -z $owner ]]; then
        say "!! 升级锁已被占用（$lock_file）。"
        say '   如确认没有其他升级进程，请人工删除该文件后重试。'
        return 1
    fi
    say "! 发现上次异常退出遗留的锁（pid=$owner），已自动清理。"
    rm -f -- "$lock_file" 2>/dev/null
    if ( set -C; : >"$lock_file" ) 2>/dev/null; then
        printf '%s\n' "$$" >"$lock_file"
        lock_held=1
        return 0
    fi
    say "!! 无法获取升级锁：$lock_file"
    return 1
}
release_lock() {
    if ((lock_held)); then
        rm -f -- "$lock_file" 2>/dev/null
        lock_held=0
    fi
}

# 收到中断时先尽力补一份“执行到哪、改了什么”的日志，再退出（正常路径的汇总不执行）。
on_interrupt() {
    local sig=$1
    trap - INT TERM HUP
    say ''
    say "[中断] 收到信号 $sig，操作被取消。"
    if ((current_step > 0 && current_step <= total)); then
        say "  中断时正在处理第 $current_step/$total 项：${PLAN_LABELS[current_step-1]}"
        if ((current_running)); then
            say '  注意：当前功能可能已写入部分改动。'
        fi
    fi
    if ((snapshot_ready)); then
        if snapshot_config "$after_snapshot"; then
            say ''
            say '── 配置变更（与执行前对比）──'
            report_changes "$before_snapshot" "$after_snapshot"
        fi
    fi
    say ''
    say "结果：已中断（信号 $sig）"
    say "日志：$log_file"
    if [[ -n $backup_file ]]; then
        say "备份：$backup_file"
        say '如需回滚，参照日志中的回滚命令恢复 config。'
    fi
    exit "$sig"
}

finish() {
    local status=$?
    trap - EXIT INT TERM HUP
    release_lock
    if [[ -n $work_dir && -d $work_dir ]]; then
        rm -rf -- "$work_dir" 2>/dev/null || true
    fi
    exit "$status"
}

setup_logging
work_dir=$(mktemp -d "$log_dir/.work.XXXXXX" 2>/dev/null) || work_dir=''
if [[ -z $work_dir ]]; then
    printf '错误：无法创建临时工作目录（日志目录：%s）。\n' "$log_dir" >&2
    exit 4
fi
trap finish EXIT
trap 'on_interrupt 130' INT
trap 'on_interrupt 143' TERM
trap 'on_interrupt 129' HUP

write_log_header
if ((auto_yes)); then
    say '已指定 --yes，跳过菜单直接执行。'
else
    choose_action
fi

if ! acquire_lock; then
    say ''
    say '已停止：同一目录已有升级进程在运行。'
    say "日志：$log_file"
    exit 6
fi

if ! preflight; then
    say ''
    say '预检未通过：已停止，未修改任何配置。'
    say "日志：$log_file"
    exit 3
fi

if ! backup_config; then
    say ''
    say '自动备份失败：已停止，未修改任何配置。'
    say '请检查磁盘/权限后重试；若现场已人工备份 config，可不加 --backup 重新执行。'
    say "日志：$log_file"
    exit 5
fi
before_snapshot=$work_dir/before.sha
after_snapshot=$work_dir/after.sha
if snapshot_config "$before_snapshot"; then
    snapshot_ready=1
fi

total=${#PLAN_FEATURES[@]}
succeeded=0
failed_index=-1
failed_status=0
run_start=$SECONDS
for ((i=0; i<total; i++)); do
    feature_rel=${PLAN_FEATURES[i]}
    feature_chambers=${PLAN_CHAMBERS[i]}
    feature_label=${PLAN_LABELS[i]}
    args=(apply --feature "$feature_rel" --config "$config_arg")
    if [[ -n $feature_chambers ]]; then
        for chamber in $feature_chambers; do
            args+=(--chamber "$chamber")
        done
    fi
    cmd_line=$runner
    for arg in "${args[@]}"; do
        cmd_line+=" $(printf '%q' "$arg")"
    done
    say ''
    say "── 步骤 $((i+1))/$total：$feature_label ──"
    if [[ -n $feature_chambers ]]; then
        say "   腔室：$feature_chambers"
    else
        say '   腔室：全部'
    fi
    say "   命令：$cmd_line"
    current_step=$((i+1))
    current_running=1
    say "   开始：$(date '+%Y-%m-%d %H:%M:%S')"
    step_start=$SECONDS
    "$runner" "${args[@]}" 2>&1 | tee -a "$log_file"
    status=${PIPESTATUS[0]}
    current_running=0
    step_elapsed=$((SECONDS - step_start))
    say "   结束：$(date '+%Y-%m-%d %H:%M:%S')（耗时 ${step_elapsed}s，退出码 $status）"
    if ((status != 0)); then
        say "   [失败] $feature_label"
        failed_index=$i
        failed_status=$status
        break
    fi
    say "   [成功] $feature_label"
    succeeded=$((succeeded + 1))
done
run_elapsed=$((SECONDS - run_start))

say ''
say '── 配置变更（与执行前对比）──'
if ((snapshot_ready)); then
    if snapshot_config "$after_snapshot"; then
        report_changes "$before_snapshot" "$after_snapshot"
    else
        say '! 无法生成执行后的校验值，略过变更清单。'
    fi
else
    say '! 执行前未能生成校验值（缺少 sha256sum），略过变更清单。'
fi

say ''
say '================ 执行结果 ================'
say "方案指纹：$PLAN_FINGERPRINT"
say "功能：成功 $succeeded/$total"
if ((forced_hash_mismatch)); then
    say '注意：功能定义曾与预置方案不一致，已按 --force 继续执行。'
fi
if ((failed_index >= 0)); then
    say "结果：失败（第 $((failed_index+1)) 项 ${PLAN_LABELS[failed_index]}，退出码 $failed_status）"
    if ((failed_index + 1 < total)); then
        say "未执行后续 $((total - failed_index - 1)) 项功能。"
    fi
else
    say '结果：成功（全部功能执行完毕）'
fi
say "耗时：${run_elapsed}s"
say "日志：$log_file"
if [[ -n $backup_file ]]; then
    say "备份：$backup_file"
else
    say '备份：未自动备份（默认跳过；现场人员手工备份）'
fi
if ((failed_index >= 0)); then
    if [[ -n $backup_file ]]; then
        say '请把上面的日志文件（及配置备份）回传工程师排查。'
    else
        say '请把上面的日志文件回传工程师排查。'
    fi
fi
say '=========================================='

if ((failed_index >= 0)); then
    final_status=$failed_status
    ((final_status == 0)) && final_status=1
else
    final_status=0
fi
if [[ -t 0 && -t 1 ]]; then
    printf '按回车键退出...'
    IFS= read -r _ || true
fi
exit "$final_status"
GEN_BODY
} >"$tmp_output"

if ! bash -n "$tmp_output"; then
    rm -f -- "$tmp_output"
    printf '生成的脚本语法检查失败，已删除临时文件。\n' >&2
    exit 1
fi
if ! mv -f -- "$tmp_output" "$output"; then
    rm -f -- "$tmp_output"
    printf '无法写入输出文件：%s\n' "$output" >&2
    exit 1
fi
chmod 755 -- "$output"

printf '\n已生成现场一键执行脚本：\n  %s\n' "$output"
printf '方案指纹：%s\n' "$plan_fingerprint"
output_size=$(wc -c <"$output" 2>/dev/null | tr -d ' ')
printf '文件大小：%s 字节\n\n' "${output_size:-未知}"
printf '现场部署（缺一不可，放在同一目录）：\n'
printf '  auto-config-update-32\n'
printf '  features/（保留子目录结构）\n'
printf '  %s（或现场实际配置目录）\n' "${config_spec:-config/}"
printf '  %s\n\n' "$(basename -- "$output")"
printf '现场执行：\n  bash %s\n\n' "$(basename -- "$output")"
printf '执行时自动生成日志：\n  <脚本目录>/logs/upgrade-<时间>-<pid>.log\n'
printf '默认不自动备份配置（现场人员手工备份）；需要时现场执行加 --backup，\n'
printf '脚本会备份到 logs/config-backup-<时间>-<pid>.tar.gz。\n'
