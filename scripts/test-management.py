#!/usr/bin/env python3
"""Isolated packaging/upgrade rollback tests; never run launchctl or Tailscale."""
import importlib.util
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import socket
import subprocess
import tempfile
import unittest
from unittest import mock

root=Path(__file__).resolve().parent.parent
spec=importlib.util.spec_from_file_location('bloomer_manage',root/'apps/agent/packaging/macos/manage.py')
manage=importlib.util.module_from_spec(spec)
spec.loader.exec_module(manage)

class ManagementTests(unittest.TestCase):
    def package(self,directory):
        package=directory/'package';package.mkdir()
        files={
            'bloomer-agent':b'synthetic executable',
            'config.example.json':b'{}',
            'manage.py':b'# synthetic manager',
            'dev.bloomer.agent.plist.template':(root/'apps/agent/packaging/macos/dev.bloomer.agent.plist.template').read_bytes(),
            'openapi.yaml':b'openapi: 3.0.3',
            'INSTALL.md':b'synthetic instructions',
        }
        for name,data in files.items():(package/name).write_bytes(data)
        (package/'manifest.json').write_text(json.dumps({'version':'1.0.0','sha256':{n:hashlib.sha256(v).hexdigest() for n,v in files.items()}}))
        return package

    def test_checksum_and_side_effect_free_plan(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory=Path(tmp);package=self.package(directory);manager=manage.Manager(directory/'home')
            plan=manager.install(package,plan=True)
            self.assertEqual(plan['version'],'1.0.0')
            self.assertFalse(manager.base.exists())
            (package/'bloomer-agent').write_bytes(b'tampered')
            with self.assertRaises(manage.InstallError):manage.verify_package(package)

    def test_serve_conflict_and_public_funnel_rejected(self):
        target='http://127.0.0.1:17890'
        config={'TCP':{'8443':{'HTTPS':True}},'Web':{'device.example.ts.net:8443':{'Handlers':{'/':{'Proxy':target}}}}}
        self.assertTrue(manage.Manager.check_serve(config,8443,target))
        with self.assertRaises(manage.InstallError):manage.Manager.check_serve(config,8443,'http://127.0.0.1:9999')
        config['AllowFunnel']={'device.example.ts.net:8443':True}
        with self.assertRaises(manage.InstallError):manage.Manager.check_serve(config,8443,target)

    def test_upgrade_failure_rolls_back_database_binary_and_plist(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory=Path(tmp);package=self.package(directory)
            class FakeManager(manage.Manager):
                running=True
                def tailscale_state(self):return 'device.example.ts.net',{}
                def loaded(self):return self.running
                def stop(self,disable=True):self.running=False
                def start(self):self.running=True;return {'phase':'P6'}
            manager=FakeManager(directory/'home');manager.tailscale=Path('/synthetic/tailscale')
            for path in [manager.base/'bin/old',manager.plist.parent,manager.base/'logs']:path.mkdir(parents=True,exist_ok=True)
            (manager.base/'bin/old/bloomer-agent').write_bytes(b'old executable')
            manager.current.symlink_to('old')
            old_plist=plistlib.dumps({'Label':manage.LABEL,'ProgramArguments':['old']});manager.plist.write_bytes(old_plist)
            database=manager.base/'agent.db';database.write_bytes(b'original data')
            token=manager.base/'access.token';token.write_bytes(b'independent persistent token')
            with socket.socket() as probe:probe.bind(('127.0.0.1',0));port=probe.getsockname()[1]
            manager.config.write_text(json.dumps({'data_dir':str(manager.base),'log_dir':str(manager.base/'logs'),'access_token_file':str(token),'listen':f'127.0.0.1:{port}','collection_enabled':False}))
            commands=[]
            def fake_run(args,check=True,timeout=30):
                args=[str(x) for x in args];commands.append(args)
                if '-backup' in args:shutil.copy2(database,args[-1])
                if '-restore' in args:shutil.copy2(args[-1],database)
                if 'serve' in args and '--bg' in args:database.write_bytes(b'new-version mutation')
                return subprocess.CompletedProcess(args,0,b'{}',b'')
            with mock.patch.object(manage,'run',fake_run),mock.patch.object(manage.sys,'platform','darwin'):
                with self.assertRaises(manage.InstallError):manager.install(package)
            self.assertEqual(os.readlink(manager.current),'old')
            self.assertEqual(database.read_bytes(),b'original data')
            self.assertEqual(token.read_bytes(),b'independent persistent token')
            self.assertEqual(manager.plist.read_bytes(),old_plist)
            self.assertTrue(manager.running)
            self.assertTrue(any('off' in command for command in commands))

if __name__=='__main__':unittest.main()
