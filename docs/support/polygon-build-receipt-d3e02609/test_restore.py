#!/usr/bin/env python3
"""Private synthetic native-proof restoration tests; no engine or owner state."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import runpy
import tempfile
from types import SimpleNamespace
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('known_restore', HERE / 'restore-known-output.py')
restore = importlib.util.module_from_spec(spec); spec.loader.exec_module(restore)


class RestoreTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='kandev-d3e-restore-test-')
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name) / 'models'; self.directory.mkdir(mode=0o700)
        self.projects = self.directory / 'projects'; self.projects.mkdir(mode=0o700)
        self.task = self.directory / restore.sha(restore.ROOT.encode()); self.task.mkdir(mode=0o700)
        self.claim = self.projects / (restore.sha(restore.PROJECT.encode()) + '.json')
        self.model = self.task / self.claim.name
        self.owner = dict(task_root=restore.ROOT, cwd=restore.ROOT, project=restore.PROJECT)
        self.write(self.claim, self.owner)
        self.write(self.model, dict(version=1, **self.owner, model={'services': {'app_base': {'image': restore.TAG}}}))
        self.proof = json.loads((HERE / 'native-output-proof.json').read_text())
        self.original = (self.claim.read_bytes(), self.model.read_bytes())

    def write(self, path, value):
        path.write_text(json.dumps(value)); path.chmod(0o600)

    def apply(self):
        return restore.restore(self.directory, json.dumps(self.proof).encode(), apply=True)

    def test_exact_native_output_restores_only_one_receipt(self):
        result = self.apply(); self.assertTrue(result['applied'])
        self.assertEqual(result['engine_commands'], 0)
        self.assertEqual((self.claim.read_bytes(), self.model.read_bytes()), self.original)
        paths = list(self.task.glob('successful-builds/*/*.json')); self.assertEqual(len(paths), 1)
        record = json.loads(paths[0].read_bytes()); self.assertEqual(record['image_id'], restore.OUTPUT_ID)
        self.assertNotEqual(record['image_id'], record['restoration']['config_digest'])
        self.assertEqual(paths[0].stat().st_mode & 0o777, 0o600)
        self.assertEqual(paths[0].stat().st_nlink, 1)

    @unittest.skipUnless(os.getuid() == 0, 'root-only private custody qualification')
    def test_root_migration_preserves_broker_uid1000(self):
        for path in (self.directory, self.projects, self.task, self.claim, self.model):
            os.chown(path, 1000, 999, follow_symlinks=False)
        self.apply()
        path = next(self.task.glob('successful-builds/*/*.json'))
        for item in (path.parent.parent, path.parent, path):
            self.assertEqual((item.stat().st_uid, item.stat().st_gid), (1000, 999))

    def test_native_proof_negative_cases(self):
        for field, value in [('ref', 'foreign-ref'), ('event_type', 0), ('error_present', True),
                             ('external_error_present', True), ('completed_at', None),
                             ('verified_output_config_id', restore.OUTPUT_ID),
                             ('exporters', [{'name': 'sibling:tag', 'type': 'image'}]),
                             ('result_descriptors', []), ('created_at', 'wrong')]:
            with self.subTest(field=field):
                proof = copy.deepcopy(self.proof); proof[field] = value
                with self.assertRaises((AssertionError, KeyError, TypeError)):
                    restore.restore(self.directory, json.dumps(proof).encode(), apply=True)
        self.assertEqual(list(self.task.glob('successful-builds/*/*.json')), [])

    def test_foreign_claim_denied(self):
        self.write(self.claim, dict(self.owner, task_root='/data/tasks/foreign'))
        with self.assertRaises(AssertionError): self.apply()

    def test_foreign_clone_denied(self):
        self.write(self.model, dict(version=1, **dict(self.owner, cwd=restore.ROOT+'/sibling'), model={}))
        with self.assertRaises(AssertionError): self.apply()

    def test_existing_receipt_is_preserved(self):
        self.apply(); path = next(self.task.glob('successful-builds/*/*.json')); before = path.read_bytes()
        with self.assertRaisesRegex(AssertionError, 'existing receipt preserved'): self.apply()
        self.assertEqual(path.read_bytes(), before)

    def test_model_only_reference_never_proves_success(self):
        self.proof.pop('exporters')
        with self.assertRaises(KeyError): self.apply()
        self.assertEqual(list(self.task.glob('successful-builds/*/*.json')), [])

    def test_insecure_claim_denied(self):
        self.claim.chmod(0o666)
        with self.assertRaises(AssertionError): self.apply()

    def test_symlink_model_denied(self):
        target = self.task/'elsewhere'; self.model.rename(target); self.model.symlink_to(target)
        with self.assertRaises(OSError): self.apply()

    def test_dry_run_has_no_writes(self):
        result = restore.restore(self.directory, json.dumps(self.proof).encode())
        self.assertFalse(result['applied']); self.assertFalse((self.task/'successful-builds').exists())

    def test_apply_rejects_changed_snapshot(self):
        with self.assertRaisesRegex(AssertionError, 'scope snapshot changed'):
            restore.restore(self.directory, json.dumps(self.proof).encode(), apply=True, enforce_snapshot=True)
        self.assertFalse((self.task/'successful-builds').exists())

    def test_wrong_native_manifest_chain_never_mints(self):
        for mutation in ('platform', 'digest', 'config', 'index-size'):
            proof = copy.deepcopy(self.proof)
            if mutation == 'platform': proof['output_manifest_chain'][1]['platform']['architecture'] = 'arm64'
            if mutation == 'digest': proof['output_manifest_chain'][1]['digest'] = restore.OUTPUT_ID
            if mutation == 'config': proof['verified_output_config_id'] = restore.PLATFORM_ID
            if mutation == 'index-size': proof['output_manifest_chain'][0]['size'] += 1
            with self.subTest(mutation=mutation), self.assertRaises(AssertionError):
                restore.restore(self.directory, json.dumps(proof).encode(), apply=True)
        self.assertFalse((self.task/'successful-builds').exists())

    def test_native_index_is_expected_id_not_observed_engine_id(self):
        result = self.apply()
        self.assertIs(result['engine_id_observed'], False)
        path = next(self.task.glob('successful-builds/*/*.json'))
        record = json.loads(path.read_bytes())
        self.assertIs(record['restoration']['engine_id_observed'], False)
        self.assertEqual(record['restoration']['image_id_binding'], 'native-completed-output-target')

    def test_symlink_receipt_directory_denied(self):
        elsewhere = self.directory/'elsewhere'; elsewhere.mkdir()
        (self.task/'successful-builds').symlink_to(elsewhere, target_is_directory=True)
        with self.assertRaises(AssertionError): self.apply()
        self.assertEqual(list(elsewhere.rglob('*.json')), [])

    def test_restored_record_accepted_by_installed_reader_without_engine(self):
        self.apply()
        path=Path('/usr/local/bin/kandev-agent-docker-operations.py')
        if not path.exists(): path=HERE.parents[1]/'scripts/kandev-agent-docker-operations.py'
        module=runpy.run_path(str(path)); calls=[]
        def check(root,cwd,project):
            self.assertEqual((str(root),str(cwd),project),(restore.ROOT,restore.ROOT,restore.PROJECT)); calls.append(True)
        broker=SimpleNamespace(MODEL_DIR=self.directory,BrokerError=RuntimeError,
            scoped_build_image=lambda image,project,service: image if (image,project,service)==(restore.TAG,restore.PROJECT,restore.SERVICE) else None,
            load_scoped_model=check)
        records=module['successful_build_images'](broker,Path(restore.ROOT),Path(restore.ROOT))
        self.assertEqual(records[restore.TAG]['image_id'],restore.OUTPUT_ID); self.assertEqual(calls,[True])

    def reader_fixture(self, identity):
        self.apply()
        path = Path('/usr/local/bin/kandev-agent-docker-operations.py')
        if not path.exists(): path = HERE.parents[1]/'scripts/kandev-agent-docker-operations.py'
        module = runpy.run_path(str(path)); calls = []
        def engine(argv, cwd, **kwargs):
            calls.append(argv)
            self.assertEqual(argv[:4], ['mock-engine', 'image', 'inspect', '--format'])
            if argv[-1] == restore.TAG:
                self.assertEqual(argv[4], '{{json .Id}}')
                return 0, json.dumps(identity), ''
            self.assertEqual(argv[-1], restore.OUTPUT_ID)
            self.assertNotIn('.Env', argv[4]); self.assertNotIn('.Cmd', argv[4])
            return 0, json.dumps(dict(Id=identity, RepoDigests=[], Os='linux', Architecture='amd64',
                Created='synthetic', Size=1, Config=dict(WorkingDir='/fixture'))), ''
        broker = SimpleNamespace(MODEL_DIR=self.directory, BrokerError=RuntimeError,
            scoped_build_image=lambda image, project, service: image if
                (image, project, service) == (restore.TAG, restore.PROJECT, restore.SERVICE) else None,
            load_scoped_model=lambda *args: {}, owned_models=lambda *args: [],
            audit_compose=lambda *args, **kwargs: None,
            DOCKER_BIN='mock-engine', run_command=engine)
        op = module['Operations'](broker, Path(restore.ROOT), Path(restore.ROOT))
        return op, calls

    def test_reader_requires_native_target_equal_mock_engine_id(self):
        op, calls = self.reader_fixture(restore.OUTPUT_ID)
        result = op.image_id_metadata(restore.OUTPUT_ID, [])
        self.assertEqual(json.loads(result['stdout'])[0]['Id'], restore.OUTPUT_ID)
        self.assertEqual(len(calls), 2)

    def test_retarget_config_and_foreign_engine_id_fail_closed(self):
        for identity in (restore.CONFIG_ID, restore.PLATFORM_ID, 'sha256:'+'f'*64):
            with self.subTest(identity=identity):
                op, calls = self.reader_fixture(identity)
                with self.assertRaisesRegex(RuntimeError, 'IMAGE_NOT_AUTHORIZED'):
                    op.image_id_metadata(restore.OUTPUT_ID, [])
                self.assertEqual(len(calls), 1)
                self.assertEqual(calls[0][-1], restore.TAG)
                # Fresh private scope for each independent fixture.
                next(self.task.glob('successful-builds/*/*.json')).unlink()

    def test_reader_foreign_root_or_clone_cannot_see_receipt(self):
        self.apply()
        path = Path('/usr/local/bin/kandev-agent-docker-operations.py')
        if not path.exists(): path = HERE.parents[1]/'scripts/kandev-agent-docker-operations.py'
        module = runpy.run_path(str(path))
        broker = SimpleNamespace(MODEL_DIR=self.directory, BrokerError=RuntimeError)
        for root, cwd in ((restore.ROOT+'/other', restore.ROOT), (restore.ROOT, restore.ROOT+'/other')):
            self.assertEqual(module['successful_build_images'](broker, Path(root), Path(cwd)), {})


if __name__ == '__main__': unittest.main(verbosity=2)
