#!/usr/bin/env bash
# 现场交互入口：与 features/、auto-config-update-32 放在同一目录。
set -u

base_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P) || exit 1
features_dir=$base_dir/features
runner=$base_dir/auto-config-update-32
config_args=()
if (($#)); then
    if (($# != 2)) || [[ $1 != --config || -z $2 ]]; then
        printf '用法: %s [--config <目录>]\n' "$0" >&2
        exit 2
    fi
    config_args=(--config "$2")
fi
if [[ ! -d $features_dir || ! -x $runner ]]; then
    printf '缺少 features/ 目录或可执行文件 auto-config-update-32：%s\n' "$base_dir" >&2
    exit 1
fi

# 读取顶层单行 YAML 标量；不尝试用正则解析 steps 等复杂 YAML 结构。
# id/description 缺失或使用多行标量时拒绝执行，避免误列出不完整的功能。
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
    if [[ $id == *$'\t'* || $description == *$'\n'* || $description == *$'\t'* ]]; then
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

chambers=(Ch1 Ch2 Ch3 Ch4 Ch5 Ch6 ChA ChB ChC ChD ChE ChF)
# 只在交互终端刷新；重定向输出或用管道输入时保留完整日志。
refresh_screen() {
    if [[ -t 0 && -t 1 && ${TERM:-dumb} != dumb ]]; then
        printf '\033[H\033[2J'
    fi
}
# 输入必须是纯十进制整数，不能让 Bash 将带前导零的输入当成八进制。
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
        printf '\n请选择要应用 %s 功能的腔室（均不选 = 全部）：\n' "${labels[feature]}"
        for ((i=0; i<${#chambers[@]}; i++)); do
            mark=' '
            ((picked[i])) && mark=x
            printf '%2d. [%s] %s\n' "$((i+1))" "$mark" "${chambers[i]}"
        done
        printf '输入编号切换，回车保存并返回：'
        if ! IFS= read -r input; then
            printf '\n输入已结束，取消操作。\n' >&2
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
    printf '\n请选择要应用的功能：\n'
    for ((i=0; i<${#paths[@]}; i++)); do
        mark=' '
        ((selected[i])) && mark=x
        printf '%2d. [%s] %s: %s' "$((i+1))" "$mark" "${labels[i]}" "${descriptions[i]}"
        if ((selected[i])); then
            printf '（腔室：%s）' "${chamber_choices[i]:-全部}"
        fi
        printf '\n'
    done
    printf '输入编号勾选/取消；r 确认执行；q 退出：'
    if ! IFS= read -r input; then
        printf '\n输入已结束，取消操作。\n' >&2
        exit 1
    fi
    case $input in
        q|Q) printf '已取消。\n'; exit 0 ;;
        r|R)
            count=0
            for value in "${selected[@]}"; do ((value)) && ((count+=1)); done
            if ((count == 0)); then
                printf '请至少选择一个功能。\n' >&2
                continue
            fi
            printf '\n即将按以下顺序执行（修改配置）：\n'
            for ((i=0; i<${#paths[@]}; i++)); do
                ((selected[i])) && printf '  %s -> %s\n' "${labels[i]}" "${chamber_choices[i]:-全部腔室}"
            done
            printf '确认执行？输入 yes 继续，其他输入取消：'
            if ! IFS= read -r input || [[ $input != yes ]]; then
                printf '已取消。\n'
                exit 0
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
                printf '无效编号：%s\n' "$input" >&2
            fi
            ;;
    esac
done

# 顺序与列表一致；遇到失败立即停止，不继续修改后续功能。
cd -- "$base_dir" || exit 1
for ((i=0; i<${#paths[@]}; i++)); do
    ((selected[i])) || continue
    args=(apply --feature "${paths[i]}" "${config_args[@]}")
    for chamber in ${chamber_choices[i]}; do
        args+=(--chamber "$chamber")
    done
    printf '\n── 执行 %s（腔室：%s）──\n' "${labels[i]}" "${chamber_choices[i]:-全部}"
    if "$runner" "${args[@]}"; then
        :
    else
        status=$?
        printf '功能 %s 执行失败（退出码 %d），已停止。\n' "${labels[i]}" "$status" >&2
        exit "$status"
    fi
done
printf '\n所选功能执行完毕。\n'
