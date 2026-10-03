# Revisa que los archivos que se van a commitear no contengan fragmentos de la
# muestra real de direcciones (data/private/emsd_muestra.csv): calle + número, o
# pares de números de una misma dirección. No contiene datos: si la muestra no
# existe en esta máquina, no revisa nada.
#
#   python scripts/check_leaks.py            # archivos en stage
#   python scripts/check_leaks.py --all      # todos los archivos versionados
#
# Lo usa .git/hooks/pre-commit (ver scripts/install_hooks.sh).
import csv, os, re, subprocess, sys, unicodedata

SAMPLE = 'data/private/emsd_muestra.csv'
# Fragmentos que existían antes de la muestra (commit inicial) o que son genéricos.
ALLOWED = {'SALVADOR SECTOR 3', 'PRADO ESTE 4200', 'ESTE 4200'}
GENERIC = re.compile(r'^[A-Z]{1,2}\d? (LT|LOTE) \d+$')
SKIP_PREFIX = ('data/',)
# Datasets sintéticos generados desde OSM: coincidencias de Mz/Lt al azar.
SKIP_FILES = {'goldenset/golden_v1.csv', 'goldenset/golden_mzlt_v1.csv', 'goldenset/golden_mzlt_v2.csv'}


def norm(s):
    s = unicodedata.normalize('NFKD', s.upper()).encode('ascii', 'ignore').decode()
    return ' '.join(re.sub(r'[^A-Z0-9]', ' ', s).split())


def fragments():
    frags = set()
    with open(SAMPLE, encoding='utf-8') as f:
        for r in csv.DictReader(f):
            w = norm(r['direccion']).split()
            for i, t in enumerate(w):
                if t.isdigit() and i >= 1:
                    frags.add(' '.join(w[max(0, i - 2):i + 1]))
                    if len(t) >= 3:
                        frags.add(w[i - 1] + ' ' + t)
                    if i + 1 < len(w) and w[i + 1].isdigit() and len(t) >= 3:
                        frags.add(t + ' ' + w[i + 1])
                    break
    return {x for x in frags if x not in ALLOWED and not GENERIC.match(x)}


def files(all_files):
    cmd = ['git', 'ls-files'] if all_files else ['git', 'diff', '--cached', '--name-only', '--diff-filter=ACM']
    out = subprocess.run(cmd, capture_output=True, text=True, encoding='utf-8').stdout.split('\n')
    return [p for p in out if p and not p.startswith(SKIP_PREFIX) and p not in SKIP_FILES]


def content(path, all_files):
    if all_files:
        with open(path, encoding='utf-8', errors='ignore') as f:
            return f.read()
    return subprocess.run(['git', 'show', ':' + path], capture_output=True, text=True,
                          encoding='utf-8', errors='ignore').stdout


def main():
    if not os.path.exists(SAMPLE):
        return 0
    frags = fragments()
    all_files = '--all' in sys.argv
    found = False
    for p in files(all_files):
        t = ' ' + norm(content(p, all_files)) + ' '
        hits = sorted(x for x in frags if ' ' + x + ' ' in t)
        if hits:
            found = True
            # Solo se muestra cuántos fragmentos hay, no cuáles: la salida puede quedar en logs.
            print(f'{p}: {len(hits)} fragmento(s) de la muestra real')
    if found:
        print('Commit bloqueado: reemplaza esos ejemplos por direcciones inventadas.')
        print('Para ver cuáles son: python scripts/check_leaks.py --show')
        if '--show' not in sys.argv:
            return 1
    if '--show' in sys.argv:
        for p in files(all_files):
            t = ' ' + norm(content(p, all_files)) + ' '
            for x in sorted(x for x in frags if ' ' + x + ' ' in t):
                print(f'  {p}: {x}')
    return 1 if found else 0


if __name__ == '__main__':
    sys.exit(main())
