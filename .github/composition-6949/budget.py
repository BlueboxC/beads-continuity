#!/usr/bin/env python3
"""Run an owned job with sampled disk, process-group RAM, swap and Go-cache guards."""

import argparse
from concurrent.futures import ThreadPoolExecutor
from contextlib import ExitStack
from decimal import Decimal, InvalidOperation
import os
from pathlib import Path
import re
import shutil
import signal
import stat
import subprocess
import sys
import tempfile
import time

GIB = 1024**3
MIB = 1024**2
RESERVE_BYTES = 20 * GIB
POLL_SECONDS = 1
CACHE_POLL_SECONDS = 5
METRIC_TIMEOUT = 3
DIRECTORY_SIZE_TIMEOUT = 10


class MonitorError(Exception):
    """A required measurement is unavailable or malformed."""


class MetricCommandError(MonitorError):
    """A failed metric command, retaining data needed for strict classification."""

    def __init__(self, message, result):
        super().__init__(message)
        self.result = result


def positive_size(value, unit):
    try:
        amount = Decimal(value)
        if not amount.is_finite() or amount <= 0:
            raise ValueError
        return max(1, int(amount * unit))
    except (InvalidOperation, ValueError, OverflowError):
        raise argparse.ArgumentTypeError("el límite debe ser un número finito positivo")


def positive_gib(value):
    return positive_size(value, GIB)


def positive_mib(value):
    return positive_size(value, MIB)


def metric_output(command, *, timeout=METRIC_TIMEOUT):
    label = ' '.join(' '.join(map(str, command)).split())[:256]
    try:
        kwargs = dict(capture_output=True, text=True, timeout=timeout)
        if command[0] == '/usr/bin/du':
            kwargs['env'] = dict(os.environ, LC_ALL='C')
        result = subprocess.run(command, **kwargs)
    except subprocess.TimeoutExpired as error:
        raise MonitorError(f"no se puede medir con {label}: límite de {timeout:g} s agotado") from error
    except (OSError, subprocess.SubprocessError) as error:
        detail = ' '.join(str(error).split())[:512]
        raise MonitorError(f"no se puede medir con {label}: {detail}") from error
    if result.returncode:
        detail = ' '.join((result.stderr or '').split())[:512]
        raise MetricCommandError(f"no se puede medir con {label}: salida {result.returncode}; {detail}", result)
    return result.stdout


def process_rss():
    groups = {}
    for line in metric_output(['/bin/ps', '-axo', 'pgid=,rss=,stat=']).splitlines():
        fields = line.split()
        if len(fields) != 3 or not all(field.isdigit() for field in fields[:2]):
            raise MonitorError("RAM: respuesta ps inválida")
        group, kib = map(int, fields[:2])
        if fields[2].startswith('Z'):
            continue
        groups[group] = groups.get(group, 0) + kib * 1024
    if not groups:
        raise MonitorError("RAM: respuesta ps vacía")
    return groups


def swap_used():
    if sys.platform.startswith('linux'):
        values = {}
        for line in Path('/proc/meminfo').read_text().splitlines():
            if line.startswith(('SwapTotal:', 'SwapFree:')):
                key, value, unit = line.split()
                if not value.isdigit() or unit != 'kB':
                    raise MonitorError("intercambio Linux: dato inválido")
                values[key.rstrip(':')] = int(value) * 1024
        if set(values) != {'SwapTotal', 'SwapFree'} or values['SwapFree'] > values['SwapTotal']:
            raise MonitorError("intercambio Linux: dato incompleto")
        return values['SwapTotal'] - values['SwapFree']
    text = metric_output(['/usr/sbin/sysctl', 'vm.swapusage'])
    match = re.search(r'\bused\s*=\s*(\d+(?:\.\d+)?)\s*([KMGT])\b', text)
    if not match:
        raise MonitorError("intercambio: respuesta sysctl inválida")
    return int(Decimal(match[1]) * (1024 ** ('KMGT'.index(match[2]) + 1)))


def go_module_cache(environment):
    saved = {}
    default_config = (Path.home() / 'Library/Application Support' if sys.platform == 'darwin'
                      else Path(environment.get('XDG_CONFIG_HOME') or Path.home() / '.config'))
    config = environment.get('GOENV') or str(default_config / 'go/env')
    if config != 'off':
        try:
            with Path(config).open('rb') as stream:
                content = stream.read(65537)
            if len(content) > 65536:
                raise MonitorError("GOENV supera 64 KiB")
            for line in content.decode('utf-8').splitlines():
                key, separator, value = line.partition('=')
                if separator and key in ('GOMODCACHE', 'GOPATH'):
                    saved[key] = value
        except FileNotFoundError:
            pass
        except (OSError, UnicodeError) as error:
            raise MonitorError("no se puede leer GOENV") from error
    value = environment.get('GOMODCACHE') or saved.get('GOMODCACHE')
    if not value:
        gopath = environment.get('GOPATH') or saved.get('GOPATH') or str(Path.home() / 'go')
        value = str(Path(gopath.split(os.pathsep)[0]) / 'pkg/mod')
    cache = Path(value)
    if not cache.is_absolute():
        raise MonitorError("GOMODCACHE debe ser una ruta absoluta")
    return cache


def _root_identity(path):
    metadata = path.lstat()
    if stat.S_ISLNK(metadata.st_mode):
        raise MonitorError("la raíz de medición se ha convertido en un enlace")
    return (metadata.st_dev, metadata.st_ino,
            getattr(metadata, 'st_birthtime_ns', getattr(metadata, 'st_birthtime', None)))


def _du_summary(output, path):
    line = output[:-1] if output.endswith('\n') else output
    blocks, separator, measured_path = line.partition('\t')
    if (not separator or not blocks.isascii() or not blocks.isdigit()
            or len(blocks) > 32 or measured_path != str(path)):
        raise MonitorError("tamaño o ruta de directorio inválidos")
    return int(blocks) * 1024


def _only_vanished_children(result, path):
    if result.returncode != 1 or not result.stderr:
        return False
    prefix, suffix = 'du: ', ': No such file or directory'
    for line in result.stderr.splitlines():
        if not line.startswith(prefix) or not line.endswith(suffix):
            return False
        child = line[len(prefix):-len(suffix)]
        if sys.platform.startswith('linux') and child.startswith("cannot access '") and child.endswith("'"):
            child = child[len("cannot access '"):-1]
        # Refuse root/ancestor failures, other diagnostics and ambiguous paths.
        if not child.startswith(str(path) + os.sep):
            return False
        relative = child[len(str(path)) + 1:]
        if not relative or '..' in Path(relative).parts:
            return False
    return True


def directory_size(path, *, missing_ok=False, deadline=None):
    try:
        path = path.resolve()
        identity = _root_identity(path)
    except FileNotFoundError as error:
        if missing_ok:
            return 0
        raise MonitorError("el directorio propio ha desaparecido") from error
    except (OSError, RuntimeError) as error:
        raise MonitorError("no se puede inspeccionar el directorio") from error
    if deadline is None:
        deadline = time.monotonic() + DIRECTORY_SIZE_TIMEOUT
    peak = 0
    for attempt in range(3):
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise MonitorError("medición du: límite total de 10 s agotado")
        failure = None
        try:
            output = metric_output(['/usr/bin/du', '-sk', str(path)], timeout=remaining)
        except MetricCommandError as error:
            failure = error
            output = error.result.stdout
        try:
            if _root_identity(path) != identity:
                raise MonitorError("la raíz de medición ha cambiado de identidad")
        except OSError as error:
            raise MonitorError("la raíz de medición ya no está disponible") from error
        if failure is not None and not _only_vanished_children(failure.result, path):
            raise failure
        peak = max(peak, _du_summary(output, path))
        if failure is None:
            return peak
        if attempt == 2:
            raise MonitorError(f"medición du inestable tras 3 lecturas: {failure}") from failure
        # Consecutive traversals can all race the same recursive cleanup.
        # Yield briefly, charging the pause to the existing total deadline.
        delay = max(0, min(.25 * (attempt + 1), deadline - time.monotonic()))
        print(f"Medición du: entrada retirada; relectura {attempt + 2}/3 de {path}; "
              f"máximo observado={peak} bytes; espera={delay:g} s.", file=sys.stderr, flush=True)
        if delay:
            time.sleep(delay)
    raise AssertionError("unreachable")


def cache_size(path):
    return directory_size(path, missing_ok=True)


def owned_directory_size(roots):
    deadline = time.monotonic() + DIRECTORY_SIZE_TIMEOUT
    return sum(directory_size(root, deadline=deadline) for root in roots)


def refusal(free, initial, budget):
    if free < RESERVE_BYTES:
        return "espacio libre por debajo de la reserva"
    if initial - free > budget:
        return "crecimiento neto del sistema de archivos superior al presupuesto"
    return None


def group_exists(group):
    try:
        os.killpg(group, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        # Darwin can report EPERM for a group containing only unreaped zombies.
        for line in metric_output(['/bin/ps', '-axo', 'pgid=,stat=']).splitlines():
            fields = line.split()
            if len(fields) != 2 or not fields[0].isdigit():
                raise MonitorError('estado de grupo inválido')
            if int(fields[0]) == group and not fields[1].startswith('Z'):
                return True
        return False


def stop(child):
    # Reap the leader before inspecting the group; descendants can remain.
    child.poll()
    if not group_exists(child.pid):
        child.wait()
        return
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except (ProcessLookupError, PermissionError):
        if group_exists(child.pid):
            raise
        child.wait()
        return
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        child.poll()
        if not group_exists(child.pid):
            break
        time.sleep(0.05)
    if group_exists(child.pid):
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except (ProcessLookupError, PermissionError):
            if group_exists(child.pid):
                raise
    child.wait()


def run(command, budget, *, dry_run=False, temporary=False,
        memory_budget=3 * GIB, swap_growth=512 * MIB, go_cache_budget=8 * GIB):
    target = Path.cwd()
    initial = shutil.disk_usage(target).free
    print(f"Disco: {target}; libres={initial / GIB:.2f} GiB; "
          f"presupuesto={budget / GIB:.2f} GiB; reserva={RESERVE_BYTES / GIB:.2f} GiB.",
          file=sys.stderr, flush=True)
    print(f"Límites: RAM grupo={memory_budget / GIB:.3f} GiB; "
          f"crecimiento swap={swap_growth / MIB:.1f} MiB; "
          f"módulos Go={go_cache_budget / GIB:.3f} GiB; GOTOOLCHAIN=local.",
          file=sys.stderr, flush=True)
    print("Protección muestreada, no cuota dura. Disco/swap/caché compartidos pueden "
          "cambiar por otros trabajos. Sólo se detiene el grupo propio; "
          "--temporary limita también su tamaño total y retira sólo sus temporales, "
          "nunca la caché compartida.",
          file=sys.stderr, flush=True)
    if any(type(value) is not int or value <= 0 for value in
           (budget, memory_budget, swap_growth, go_cache_budget)):
        print("RECHAZADO: los límites deben ser bytes enteros positivos.", file=sys.stderr)
        return 125
    if initial < RESERVE_BYTES + budget:
        print("RECHAZADO: faltan bytes para presupuesto más reserva.", file=sys.stderr)
        return 125
    environment = os.environ.copy()
    environment['GOTOOLCHAIN'] = 'local'
    try:
        cache = go_module_cache(environment)
        environment['GOMODCACHE'] = str(cache)
        process_rss()[os.getpgrp()]
        initial_swap = swap_used()
        initial_cache = cache_size(cache)
        if initial_cache > go_cache_budget:
            raise MonitorError("GOMODCACHE supera su límite; se conserva sin borrar")
    except (MonitorError, OSError, KeyError) as error:
        print(f"RECHAZADO: {error}; no se inicia el trabajo.", file=sys.stderr, flush=True)
        return 125
    print(f"GOMODCACHE: {cache}; tamaño={initial_cache / GIB:.3f} GiB; "
          f"swap inicial={initial_swap / MIB:.1f} MiB.", file=sys.stderr, flush=True)
    if dry_run:
        print("Preflight correcto; simulación sin ejecutar el trabajo.", file=sys.stderr)
        return 0

    cancelled = []
    previous = {}
    for sig in (signal.SIGINT, signal.SIGTERM):
        previous[sig] = signal.signal(sig, lambda number, frame: cancelled.append(number))
    child = None
    worker = None
    owned_cleanup = ExitStack()
    owned_roots = ()
    owned_future = None
    try:
        if temporary:
            scratch = Path(owned_cleanup.enter_context(
                tempfile.TemporaryDirectory(prefix='workspace-job-')))
            build_cache = Path(owned_cleanup.enter_context(
                tempfile.TemporaryDirectory(prefix='workspace-job-cache-', dir=scratch.parent)))
            owned_roots = (scratch, build_cache)
            environment.update(TMPDIR=str(scratch), TMP=str(scratch), TEMP=str(scratch))
            for name, directory in (('GOTMPDIR', build_cache / 'go-tmp'),
                                    ('GOCACHE', build_cache / 'go-build'),
                                    ('GOLANGCI_LINT_CACHE', build_cache / 'golangci-lint')):
                directory.mkdir()
                environment[name] = str(directory)
            print(f"Temporales/cachés de compilación propios: {scratch}; "
                  f"cachés en {build_cache}; se retiran al terminar.", file=sys.stderr, flush=True)
            try:
                if owned_directory_size(owned_roots) > budget:
                    raise MonitorError("los temporales propios superan el presupuesto")
            except MonitorError as error:
                print(f"RECHAZADO: {error}; no se inicia el trabajo.", file=sys.stderr)
                return 125
        if cancelled:
            return 128 + cancelled[0]
        child = subprocess.Popen(command, start_new_session=True, env=environment)
        # Directory traversal runs off the RAM/swap polling path, with a bounded command timeout.
        worker = ThreadPoolExecutor(max_workers=2)
        future = worker.submit(cache_size, cache)
        next_cache = time.monotonic() + CACHE_POLL_SECONDS
        if owned_roots:
            owned_future = worker.submit(owned_directory_size, owned_roots)
        next_owned = time.monotonic() + POLL_SECONDS
        while child.poll() is None:
            if cancelled:
                return 128 + cancelled[0]
            free = shutil.disk_usage(target).free
            reason = refusal(free, initial, budget)
            groups = process_rss()
            try:
                ram = groups[child.pid]
            except KeyError:
                if child.poll() is not None:
                    break
                raise MonitorError("no se puede medir la RAM del grupo propio")
            if ram > memory_budget:
                reason = "RAM del grupo superior al límite"
            if swap_used() - initial_swap > swap_growth:
                reason = "crecimiento global de intercambio superior al límite"
            if future is not None and future.done():
                if future.result() > go_cache_budget:
                    reason = "GOMODCACHE supera su límite; se conserva sin borrar"
                future = None
            if owned_future is not None and owned_future.done():
                if owned_future.result() > budget:
                    reason = "tamaño de temporales propios superior al presupuesto"
                owned_future = None
            if reason:
                print(f"DETENIDO: {reason}; libres={free / GIB:.2f} GiB; "
                      f"RAM grupo={ram / MIB:.1f} MiB.", file=sys.stderr, flush=True)
                return 124
            if future is None and time.monotonic() >= next_cache:
                future = worker.submit(cache_size, cache)
                next_cache = time.monotonic() + CACHE_POLL_SECONDS
            if owned_roots and owned_future is None and time.monotonic() >= next_owned:
                owned_future = worker.submit(owned_directory_size, owned_roots)
                next_owned = time.monotonic() + POLL_SECONDS
            time.sleep(POLL_SECONDS)
        # Retire any owned background children before closeout and temporary cleanup.
        stop(child)
        if cancelled:
            return 128 + cancelled[0]
        pending_owned_overrun = owned_future is not None and owned_future.result() > budget
        pending_cache_overrun = future is not None and future.result() > go_cache_budget
        reason = refusal(shutil.disk_usage(target).free, initial, budget)
        if swap_used() - initial_swap > swap_growth:
            reason = "crecimiento global de intercambio superior al límite"
        if pending_cache_overrun or cache_size(cache) > go_cache_budget:
            reason = "GOMODCACHE supera su límite; se conserva sin borrar"
        if pending_owned_overrun or (owned_roots and owned_directory_size(owned_roots) > budget):
            reason = "tamaño de temporales propios superior al presupuesto"
        if cancelled:
            return 128 + cancelled[0]
        if reason:
            print(f"DETENIDO al cierre: {reason}", file=sys.stderr, flush=True)
            return 124
        return child.returncode if child.returncode >= 0 else 128 - child.returncode
    except (MonitorError, OSError) as error:
        if isinstance(error, FileNotFoundError):
            print(f"No se puede iniciar el comando: {error}", file=sys.stderr)
            return 127
        print(f"DETENIDO: medición o ejecución no disponible: {error}", file=sys.stderr, flush=True)
        return 124
    finally:
        try:
            if child is not None:
                stop(child)
            if worker is not None:
                worker.shutdown(wait=True, cancel_futures=True)
            owned_cleanup.close()
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--budget-gib', type=positive_gib, required=True)
    parser.add_argument('--memory-gib', type=positive_gib, default=3 * GIB)
    parser.add_argument('--swap-growth-mib', type=positive_mib, default=512 * MIB)
    parser.add_argument('--go-cache-gib', type=positive_gib, default=8 * GIB)
    parser.add_argument('--dry-run', action='store_true')
    parser.add_argument('--temporary', action='store_true', help='aislar, limitar por --budget-gib y retirar sólo temporales propios')
    parser.add_argument('command', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ['--'] else args.command
    if not command:
        parser.error('falta el comando después de --')
    return run(command, args.budget_gib, dry_run=args.dry_run, temporary=args.temporary,
               memory_budget=args.memory_gib, swap_growth=args.swap_growth_mib,
               go_cache_budget=args.go_cache_gib)


if __name__ == '__main__':
    raise SystemExit(main())
