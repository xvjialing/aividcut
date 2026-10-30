import binascii
import json
from pathlib import Path
import struct
import tempfile
import unittest
from unittest.mock import patch
import xml.etree.ElementTree as ET
import zlib
from prepare_windows_msix import prepare, verify_prepared, sha, NAMESPACE, UAP10
from attach_windows_ocr import attach

def pe_fixture(machine=0x8664):
    data=bytearray(128);data[:2]=b'MZ';struct.pack_into('<I',data,60,80);data[80:84]=b'PE\0\0';struct.pack_into('<H',data,84,machine);return bytes(data)
def png_fixture(size):
    def chunk(tag,data):
        return struct.pack('>I',len(data))+tag+data+struct.pack('>I',binascii.crc32(tag+data)&0xffffffff)
    raw=b''.join(b'\0'+bytes([20,35,60,255])*size for _ in range(size))
    return b'\x89PNG\r\n\x1a\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',size,size,8,6,0,0,0))+chunk(b'IDAT',zlib.compress(raw))+chunk(b'IEND',b'')
class MSIXPreparationTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name);self.candidate=self.root/'candidate';self.candidate.mkdir()
        (self.candidate/'AIVidCut.exe').write_bytes(pe_fixture())
        self.logos={}
        for size in (44,50,150):
            path=self.root/f'logo{size}.png';path.write_bytes(png_fixture(size));self.logos[size]=path
        self.args=dict(identity='AIVidCut.LocalFixture',publisher='CN=本机构建夹具 & 测试',publisher_name='本机测试',version='0.1.0.0',arch='x64',logos=self.logos)
    def add_ocr(self):
        project=Path(__file__).resolve().parent.parent
        data=pe_fixture();(self.candidate/'AIVidCutOCR.exe').write_bytes(data)
        record=dict(schemaVersion=1,component='AIVidCutOCR',engine='windows-media-ocr',arch='x64',source='native/ocr-windows/main.cpp',sourceSHA256=sha((project/'native/ocr-windows/main.cpp').read_bytes()),artifactSHA256=sha(data),nativeCompileCompleted=True,packageIdentityRequired=True,runtimeVerified=False)
        (self.candidate/'ocr-build-info.json').write_text(json.dumps(record));return record
    def test_manifest_identity_escaped_and_no_execution_or_permissions_added(self):
        self.add_ocr();out=self.root/'msix';report=prepare(self.candidate,out,**self.args)
        xml=ET.parse(out/'Payload/AppxManifest.xml').getroot()
        identity=xml.find(f'{{{NAMESPACE}}}Identity');self.assertEqual(identity.attrib['Publisher'],self.args['publisher'])
        app=xml.find(f'{{{NAMESPACE}}}Applications/{{{NAMESPACE}}}Application')
        self.assertEqual(app.attrib[f'{{{UAP10}}}TrustLevel'],'mediumIL')
        self.assertEqual([n.attrib['Name'] for n in xml.find(f'{{{NAMESPACE}}}Capabilities')],['runFullTrust'])
        self.assertFalse(report['makeAppxExecuted']);self.assertFalse(report['installed']);self.assertFalse(report['runtimeVerified'])
        self.assertEqual((out/'Payload/AIVidCut.exe').read_bytes(),pe_fixture())
        verify_prepared(out)
        (out/'Payload/extra.txt').write_text('新增未核对内容')
        with self.assertRaises(ValueError):verify_prepared(out)
        (out/'Payload/extra.txt').unlink()
        (out/'Payload/AppxManifest.xml').write_text('修改身份')
        with self.assertRaises(ValueError):verify_prepared(out)
    def test_missing_ocr_requires_explicit_incomplete_mode(self):
        with self.assertRaises(ValueError):prepare(self.candidate,self.root/'a',**self.args)
        report=prepare(self.candidate,self.root/'b',allow_missing_ocr=True,**self.args)
        self.assertFalse(report['ocrPayloadPresent'])
    def test_rejects_architecture_source_hash_and_candidate_case_collision(self):
        record=self.add_ocr();record['sourceSHA256']='0'*64;(self.candidate/'ocr-build-info.json').write_text(json.dumps(record))
        with self.assertRaises(ValueError):prepare(self.candidate,self.root/'a',**self.args)
        self.add_ocr();(self.candidate/'alien.dll').write_bytes(pe_fixture(0xAA64))
        with self.assertRaises(ValueError):prepare(self.candidate,self.root/'b',**self.args)
        (self.candidate/'alien.dll').unlink();(self.candidate/'README').write_text('a');(self.candidate/'readme').write_text('b')
        # 大小写不敏感文件系统不能创建冲突夹具，不强求该平台的造假行为。
        if len(list(self.candidate.glob('*')))>=5:
            with self.assertRaises(ValueError):prepare(self.candidate,self.root/'c',**self.args)
        else:
            # 在大小写不敏感宿主模拟来自另一文件系统的冲突目录项。
            with patch('prepare_windows_msix.os.walk',return_value=[(str(self.candidate),[],['AIVidCut.exe','AIVIDCUT.EXE'])]):
                with self.assertRaises(ValueError):prepare(self.candidate,self.root/'c',**self.args)
    def test_rejects_links_nested_output_and_bad_logo_crc(self):
        self.add_ocr()
        with self.assertRaises(ValueError):prepare(self.candidate,self.candidate/'nested',**self.args)
        link=self.candidate/'outside';link.symlink_to(self.root/'logo44.png')
        with self.assertRaises(ValueError):prepare(self.candidate,self.root/'a',**self.args)
        link.unlink();data=bytearray(self.logos[44].read_bytes());data[-1]^=1;self.logos[44].write_bytes(data)
        with self.assertRaises(ValueError):prepare(self.candidate,self.root/'b',**self.args)
    def test_attach_validates_source_and_refuses_overwrite(self):
        record=self.add_ocr();dest=self.root/'new-candidate';dest.mkdir();(dest/'AIVidCut.exe').write_bytes(pe_fixture())
        result=attach(self.candidate,dest,'amd64');self.assertEqual(result,record)
        with self.assertRaises(ValueError):attach(self.candidate,dest,'amd64')
        wrong=self.root/'wrong';wrong.mkdir();(wrong/'AIVidCut.exe').write_bytes(pe_fixture())
        with self.assertRaises(ValueError):attach(self.candidate,wrong,'arm64')
if __name__=='__main__':unittest.main()
