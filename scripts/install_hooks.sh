#!/bin/sh
# Instala el hook de pre-commit que bloquea fragmentos de la muestra real.
#   sh scripts/install_hooks.sh
set -e
hook="$(git rev-parse --git-path hooks)/pre-commit"
cat > "$hook" <<'EOF'
#!/bin/sh
python scripts/check_leaks.py
EOF
chmod +x "$hook"
echo "hook instalado en $hook"
