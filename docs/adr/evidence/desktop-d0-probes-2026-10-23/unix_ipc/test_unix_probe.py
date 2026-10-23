import json,os,socket,stat,struct,tempfile,threading,time,unittest
from pathlib import Path
from unix_probe import Endpoint,Rejected,encode_frame,read_frame,peer_uid,serve_one

class NativeUnixProbeTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory(prefix="td0-ipc-",dir="/private/tmp")
        self.root=Path(self.temp.name)
        self.addCleanup(lambda root=self.root:self.assertFalse(root.exists(),"owned temporary root remains"))
        self.addCleanup(self.temp.cleanup)
    def pair(self):
        a,b=socket.socketpair();self.addCleanup(a.close);self.addCleanup(b.close);return a,b
    def endpoint(self,root=None):
        e=Endpoint(root or self.root);self.addCleanup(e.close);e.bind();return e
    def test_actual_runtime_and_socket_modes(self):
        e=self.endpoint();self.assertEqual(stat.S_IMODE(self.root.stat().st_mode),0o700);self.assertEqual(stat.S_IMODE(e.path.stat().st_mode),0o600)
    def test_actual_kernel_peer_uid_matches_current_process(self):
        a,b=self.pair();self.assertEqual(peer_uid(a),os.getuid());self.assertEqual(peer_uid(b),os.getuid())
    def test_insecure_directory_rejected(self):
        self.root.chmod(0o755)
        with self.assertRaises(Rejected):self.endpoint()
    def test_symlink_directory_rejected(self):
        link=self.root/"alias";link.symlink_to(self.root,target_is_directory=True)
        with self.assertRaises(Rejected):self.endpoint(link)
    def test_preexisting_socket_path_is_never_unlinked(self):
        target=self.root/"daemon.sock";target.write_text("owned stand-in for foreign resource")
        with self.assertRaises(Rejected):self.endpoint()
        self.assertEqual(target.read_text(),"owned stand-in for foreign resource")
    def test_cleanup_does_not_remove_replacement_inode(self):
        e=self.endpoint();e.path.unlink();e.path.write_text("replacement fixture");e.close();self.assertEqual(e.path.read_text(),"replacement fixture")
    def test_os_path_length_rejected_before_bind(self):
        long=self.root/("a"*100);long.mkdir(mode=0o700)
        with self.assertRaises(Rejected):self.endpoint(long)
    def test_fragmented_frame_reassembles_without_truncation(self):
        a,b=self.pair();message={"version":1,"kind":"hello","payload":{}};a.sendall(encode_frame(message))
        class Fragmented:
            def settimeout(self,t):b.settimeout(t)
            def recv(self,n):return b.recv(min(n,1))
        self.assertEqual(read_frame(Fragmented(),time.monotonic()+0.25),message)
    def test_oversize_rejected_before_body_is_read(self):
        a,b=self.pair();a.sendall(struct.pack("!I",65537)+b"body")
        with self.assertRaises(Rejected):read_frame(b,time.monotonic()+0.25)
        self.assertEqual(b.recv(4),b"body")
    def test_truncated_frame_rejected_whole(self):
        a,b=self.pair();a.sendall(struct.pack("!I",20)+b"{}");a.shutdown(socket.SHUT_WR)
        with self.assertRaises(Rejected):read_frame(b,time.monotonic()+0.25)
    def test_duplicate_json_keys_rejected(self):
        a,b=self.pair();body=b'{"version":1,"version":2}';a.sendall(struct.pack("!I",len(body))+body)
        with self.assertRaises(Rejected):read_frame(b,time.monotonic()+0.25)
    def test_expired_total_deadline_rejected_without_wait(self):
        a,b=self.pair()
        with self.assertRaises(Rejected):read_frame(b,time.monotonic()-1)
    def roundtrip(self,observation,version=1):
        endpoint=self.endpoint();result=[]
        def worker():
            try:
                with endpoint.socket.accept()[0] as conn:result.append(serve_one(conn,("run-a","generation-a"),os.getuid(),time.monotonic()+0.25))
            except (Rejected,OSError) as error:result.append(type(error).__name__)
        thread=threading.Thread(target=worker);thread.start()
        try:
            with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as client:
                client.settimeout(1);client.connect(str(endpoint.path));client.sendall(encode_frame({"version":version,"kind":"hello","payload":{}}))
                try:
                    read_frame(client,time.monotonic()+0.25);client.sendall(encode_frame(observation));read_frame(client,time.monotonic()+0.25)
                except (Rejected,OSError):pass
        finally:
            thread.join()
            endpoint.close()
        return result
    def observation(self,**updates):
        payload=dict(run="run-a",generation="generation-a",event="turn_boundary",source_event_id="event-a",host="synthetic-host",host_version="fixture");payload.update(updates)
        return dict(version=1,kind="observation",payload=payload)
    def test_actual_observation_round_trip(self):
        self.assertEqual(self.roundtrip(self.observation()),["observation_received"])
    def test_unknown_protocol_version_rejected(self):
        self.assertEqual(self.roundtrip(self.observation(),version=2),["Rejected"])
    def test_boolean_version_is_not_integer_protocol_version(self):
        self.assertEqual(self.roundtrip(self.observation(),version=True),["Rejected"])
    def test_stale_generation_rejected(self):
        self.assertEqual(self.roundtrip(self.observation(generation="old")),["Rejected"])
    def test_terminal_input_has_no_handler(self):
        message=dict(version=1,kind="terminal_input",payload={"text":"never execute"});self.assertEqual(self.roundtrip(message),["Rejected"])
    def test_unexpected_content_field_rejected(self):
        self.assertEqual(self.roundtrip(self.observation(prompt="synthetic forbidden field")),["Rejected"])
    def test_kernel_uid_mismatch_branch_is_not_cross_user_certification(self):
        a,b=self.pair()
        with self.assertRaises(Rejected):serve_one(b,("run-a","generation-a"),os.getuid()+1,time.monotonic()+0.25)

    def test_withheld_observation_expires_on_actual_socket_deadline(self):
        a,b=self.pair();outcome=[]
        def worker():
            try:serve_one(b,("run-a","generation-a"),os.getuid(),time.monotonic()+0.25)
            except Rejected as error:outcome.append(str(error))
            finally:b.close()
        thread=threading.Thread(target=worker);thread.start()
        try:
            a.sendall(encode_frame(dict(version=1,kind="hello",payload={})))
            self.assertEqual(read_frame(a,time.monotonic()+1)["kind"],"hello_ack")
            a.settimeout(1);self.assertEqual(a.recv(1),b"")
        finally:a.close();thread.join()
        self.assertEqual(outcome,["total exchange deadline expired"])

    def test_zero_size_header_rejected(self):
        a,b=self.pair();a.sendall(struct.pack("!I",0))
        with self.assertRaises(Rejected):read_frame(b,time.monotonic()+0.25)

    def test_invalid_utf8_and_nonfinite_number_rejected(self):
        for body in (b"\xff",b'{"value":NaN}'):
            a,b=self.pair();a.sendall(struct.pack("!I",len(body))+body)
            with self.assertRaises(Rejected):read_frame(b,time.monotonic()+0.25)

    def test_exact_frame_limit_is_not_truncated(self):
        base=len(encode_frame({"data":""}))-4
        message={"data":"x"*(65536-base)}
        frame=encode_frame(message);self.assertEqual(len(frame),65540)

    def test_encoder_rejects_over_limit_whole(self):
        with self.assertRaises(Rejected):encode_frame({"data":"x"*65536})

    def test_all_control_operations_have_no_handler(self):
        for kind in ("approval_decision","memory_adopt","process_stop"):
            with self.subTest(kind=kind):self.assertEqual(self.roundtrip(dict(version=1,kind=kind,payload={})),["Rejected"])

    def test_peer_claim_in_json_does_not_replace_kernel_identity(self):
        message=self.observation();message["payload"]["peer_uid"]="claimed-owner"
        self.assertEqual(self.roundtrip(message),["Rejected"])

    def test_preexisting_live_socket_is_not_unlinked_or_closed(self):
        original=self.endpoint();inode=original.path.stat().st_ino
        with self.assertRaises(Rejected):self.endpoint()
        self.assertEqual(original.path.stat().st_ino,inode)
        self.assertGreaterEqual(original.socket.fileno(),0)

    def test_symlink_socket_path_is_refused_without_touching_target(self):
        target=self.root/"target";target.write_text("fixture target")
        path=self.root/"daemon.sock";path.symlink_to(target)
        with self.assertRaises(Rejected):self.endpoint()
        self.assertTrue(path.is_symlink());self.assertEqual(target.read_text(),"fixture target")

    def test_separate_same_uid_client_process_uses_actual_kernel_peer_identity(self):
        import subprocess,sys
        endpoint=self.endpoint()
        code="""import socket,sys,json,struct,os
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);s.settimeout(1);s.connect(sys.argv[1])
def exact(n):
 b=b''
 while len(b)<n:
  part=s.recv(n-len(b))
  if not part:raise RuntimeError('partial fixture reply')
  b+=part
 return b
def send(kind,payload):
 b=json.dumps(dict(version=1,kind=kind,payload=payload)).encode();s.sendall(struct.pack('!I',len(b))+b)
def read():return json.loads(exact(struct.unpack('!I',exact(4))[0]))
send('hello',{});assert read()['kind']=='hello_ack'
send('observation',dict(run='run-a',generation='generation-a',event='turn_boundary',source_event_id='event-a',host='synthetic-host',host_version='fixture'))
assert read()['kind']=='receipt';assert os.getpid()!=int(sys.argv[2]);s.close()
"""
        child=subprocess.Popen([sys.executable,"-I","-S","-c",code,str(endpoint.path),str(os.getpid())],cwd=self.root,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        try:
            with endpoint.socket.accept()[0] as conn:
                self.assertEqual(peer_uid(conn),os.getuid())
                self.assertEqual(serve_one(conn,("run-a","generation-a"),os.getuid(),time.monotonic()+0.25),"observation_received")
        finally:
            stdout,stderr=child.communicate(timeout=2)
        self.assertEqual(child.returncode,0,stderr.decode())
        self.assertEqual(stdout,b"")

if __name__=="__main__":unittest.main()
