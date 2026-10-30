"""声明收集的身份/边界回归；不联网或读取真实账户。"""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('notices', Path(__file__).with_name('collect_dependency_notices.py'))
notices = importlib.util.module_from_spec(spec)
spec.loader.exec_module(notices)


class NoticeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.front = self.root / 'frontend'
        self.package = self.front / 'node_modules/example'
        self.package.mkdir(parents=True)
        (self.package / 'package.json').write_text(json.dumps({'name': 'example', 'version': '1.0.0', 'license': 'MIT'}))
        (self.front / 'package.json').write_text(json.dumps({'packageManager': 'pnpm@12.15.0'}))
        lock = self.front / 'pnpm-lock.yaml'
        self.integrity = 'sha512-Zml4dHVyZQ=='
        lock.write_text(json.dumps({'lockfileVersion': '9.0',
                                   'importers': {'.': {'dependencies': {'example': {'specifier': '1.0.0', 'version': '1.0.0'}}}},
                                   'packages': {'example@1.0.0': {'resolution': {'integrity': self.integrity}}}}))
        self.dist = self.front / 'dist'
        self.dist.mkdir()
        (self.dist / 'main.js').write_text('export const value = 1;')
        self.inventory = {'schemaVersion': 2, 'lockfileName': 'pnpm-lock.yaml',
                          'lockfileSHA256': notices.digest(lock.read_bytes()), 'packageManager': 'pnpm@12.15.0',
                          'packages': [{'name': 'example', 'version': '1.0.0', 'integrity': self.integrity, 'lockPath': 'node_modules/example'}],
                          'chunks': [{'file': 'main.js', 'bytes': (self.dist / 'main.js').stat().st_size, 'sha256': notices.digest((self.dist / 'main.js').read_bytes())}]}
        (self.dist / 'dependency-inventory.json').write_text(json.dumps(self.inventory))
        self.artifact = self.root / 'app'
        self.artifact.write_bytes(b'fake binary for metadata fixture')
        self.output = self.root / 'output'
        self.output.mkdir()
        self.config = {'projectRoot': str(self.root), 'frontendRoot': str(self.front), 'frontendInventory': str(self.dist / 'dependency-inventory.json'), 'artifacts': [{'name': 'desktop', 'path': str(self.artifact)}]}

    def collect(self):
        with patch.object(notices, 'command_json', side_effect=[[], {'GoVersion': 'go-test', 'Deps': [], 'Settings': []}]):
            return notices.collect(self.config, self.output)

    def update_inventory(self):
        (self.dist / 'dependency-inventory.json').write_text(json.dumps(self.inventory))

    def test_missing_text_is_not_approval_or_fabricated_mit_text(self):
        result = self.collect()
        self.assertEqual(result['missingTextComponents'], 1)
        self.assertFalse(result['licenseApproval'])
        self.assertEqual(result['components'][0]['texts'], [])

    def test_original_text_and_patents_are_copied_without_source_files(self):
        (self.package / 'LICENSE').write_text('original license')
        (self.package / 'PATENTS').write_text('patent statement')
        (self.package / 'license.js').write_text('private implementation')
        result = self.collect()
        texts = result['components'][0]['texts']
        self.assertEqual({item['sourceName'] for item in texts}, {'LICENSE', 'PATENTS'})
        for item in texts:
            data = (self.output / item['path']).read_bytes()
            self.assertEqual(hashlib.sha256(data).hexdigest(), item['sha256'])
        self.assertNotIn(str(self.root), (self.output / 'inventory.json').read_text())

    def test_changed_chunk_and_path_escape_rejected(self):
        (self.dist / 'main.js').write_text('changed output')
        with self.assertRaisesRegex(ValueError, 'chunk'):
            self.collect()
        self.inventory['chunks'][0]['file'] = '../secret.js'
        self.update_inventory()
        with self.assertRaisesRegex(ValueError, '路径'):
            self.collect()

    def test_installed_version_and_linked_license_rejected(self):
        (self.package / 'package.json').write_text(json.dumps({'name': 'example', 'version': '2.0.0'}))
        with self.assertRaisesRegex(ValueError, '元数据'):
            self.collect()
        (self.package / 'package.json').write_text(json.dumps({'name': 'example', 'version': '1.0.0'}))
        target = self.root / 'private.txt'
        target.write_text('not a dependency license')
        (self.package / 'LICENSE').symlink_to(target)
        with self.assertRaisesRegex(ValueError, '链接'):
            self.collect()

    def test_supplement_requires_fixed_component_and_hash(self):
        source = self.root / 'supplement.txt'
        source.write_text('exact upstream text')
        self.config['licenseSources'] = [{'component': 'npm:example@1.0.0', 'file': 'supplement.txt', 'sha256': '0' * 64, 'sourceURL': 'https://example.test/revision/LICENSE', 'commit': 'revision'}]
        with self.assertRaisesRegex(ValueError, 'SHA256'):
            self.collect()
        self.config['licenseSources'][0]['sha256'] = notices.digest(source.read_bytes())
        result = self.collect()
        self.assertEqual(result['missingTextComponents'], 0)
        self.assertEqual(result['components'][0]['supplementalSource']['commit'], 'revision')

    def test_changed_artifact_during_metadata_query_rejected(self):
        def command(*_args, **_kwargs):
            self.artifact.write_bytes(b'replaced during inspection')
            return {'GoVersion': 'go-test', 'Deps': []}
        calls = 0
        def replace_on_second(*args, **kwargs):
            nonlocal calls
            calls += 1
            if calls == 1:
                return []
            return command()
        with patch.object(notices, 'command_json', side_effect=replace_on_second):
            with self.assertRaisesRegex(ValueError, '产物变化'):
                notices.collect(self.config, self.output)

    def test_external_binding_tracks_final_signature_bytes_and_texts(self):
        (self.package / 'LICENSE').write_text('original license')
        collected = self.collect()
        self.artifact.write_bytes(b'final signed binary bytes')
        output = self.root / 'binding.json'
        bound = notices.bind(self.artifact, self.output, output)
        self.assertEqual(bound['artifactSHA256'], notices.digest(self.artifact.read_bytes()))
        self.assertNotEqual(bound['artifactSHA256'], collected['artifacts'][0]['sha256'])
        self.assertFalse(bound['licenseApproval'])
        with self.assertRaisesRegex(ValueError, '覆盖'):
            notices.bind(self.artifact, self.output, self.artifact)
        text = collected['components'][0]['texts'][0]
        (self.output / text['path']).write_text('changed license')
        with self.assertRaisesRegex(ValueError, '原文已变化'):
            notices.bind(self.artifact, self.output, output)

    def test_pnpm_virtual_store_layout_and_external_link_boundary(self):
        stored = self.front / 'node_modules/.pnpm/example@1.0.0/node_modules/example'
        stored.parent.mkdir(parents=True)
        self.package.rename(stored)
        self.package.symlink_to(stored, target_is_directory=True)
        self.inventory['packages'][0]['lockPath'] = stored.relative_to(self.front).as_posix()
        self.update_inventory()
        self.assertEqual(self.collect()['components'][0]['version'], '1.0.0')
        outside = self.root / 'external-package'
        stored.rename(outside)
        stored.symlink_to(outside, target_is_directory=True)
        with self.assertRaisesRegex(ValueError, '根目录'):
            self.collect()

    def test_lock_digest_and_package_integrity_mismatch_rejected(self):
        self.inventory['packages'][0]['integrity'] = 'sha512-b3RoZXI='
        self.update_inventory()
        with self.assertRaisesRegex(ValueError, '元数据'):
            self.collect()
        lock = self.front / 'pnpm-lock.yaml'
        lock.write_text(lock.read_text() + '\n')
        with self.assertRaisesRegex(ValueError, '锁文件'):
            self.collect()

    def test_package_manager_document_is_not_mistaken_for_application(self):
        lock = self.front / 'pnpm-lock.yaml'
        bootstrap = {'lockfileVersion': '9.0', 'importers': {'.': {'packageManagerDependencies': {'pnpm': {'version': '12.15.0'}}}}, 'packages': {}}
        lock.write_text(json.dumps(bootstrap) + '\n---\n' + lock.read_text())
        self.inventory['lockfileSHA256'] = notices.digest(lock.read_bytes())
        self.update_inventory()
        self.assertEqual(self.collect()['components'][0]['name'], 'example')


if __name__ == '__main__':
    unittest.main()
