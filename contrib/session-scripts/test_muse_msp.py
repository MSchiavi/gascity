import importlib.util
import pathlib
import unittest
import tempfile
import threading
from unittest.mock import patch
import io

spec = importlib.util.spec_from_file_location('msp', pathlib.Path(__file__).with_name('gc-session-muse-msp'))
# Extensionless executable needs an explicit source loader.
from importlib.machinery import SourceFileLoader
spec = importlib.util.spec_from_loader('msp', SourceFileLoader('msp', str(pathlib.Path(__file__).with_name('gc-session-muse-msp'))))
msp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(msp)


class Receipts(unittest.TestCase):
    def host(self, folder):
        host = msp.Host.__new__(msp.Host)
        host.state_path = pathlib.Path(folder) / 'state.json'
        host.lock = threading.RLock()
        host.state = {'provider_session_id': 's', 'receipts': {}, 'events': [], 'ready': True}
        return host

    def test_reasoning_is_exact_turn_option_not_serve_flag(self):
        self.assertEqual(msp.serve_argv('muse', {'reasoning': 'max'}, {}), ['muse', 'serve'])
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            host.state['effective_options'] = {'reasoning': 'max'}
            request = {'command_id': msp.uuid7(), 'provider_session_id': 's', 'text': 'hello',
                       'fence': {'session_id': 'gc-test', 'continuation_epoch': '1', 'runtime_token': 'token'}}
            def rpc(method, params):
                self.assertEqual(method, 'turn/start')
                self.assertEqual(params['reasoningEffort'], 'max')
                return {'commandId': params['commandId'], 'status': 'accepted', 'turnId': 't', 'disposition': 'started'}
            host.rpc = rpc
            self.assertEqual(host.admit(request)['state'], 'accepted')

    def test_admission_is_not_completion(self):
        receipt = msp.admission({'commandId': 'c', 'status': 'accepted', 'turnId': 't', 'disposition': 'queued'}, 'c', 's')
        self.assertEqual(receipt['state'], 'accepted')
        self.assertNotIn('terminal', receipt)

    def test_wrong_command_fails_closed(self):
        with self.assertRaises(ValueError):
            msp.admission({'commandId': 'other', 'status': 'accepted'}, 'c', 's')

    def test_rejection_preserves_known_fields_only(self):
        result = msp.rejection({'code': -32030, 'message': 'private', 'data': {'kind': 'commandRejected', 'reason': 'abandoned', 'commandId': 'c', 'retryable': False, 'secret': 'private'}}, 'c', 's')
        self.assertEqual(result['state'], 'not_admitted')
        self.assertEqual(result['reason'], 'abandoned')
        self.assertNotIn('private', str(result))

    def test_unbound_rejection_is_unknown(self):
        result = msp.rejection({'code': -32030, 'data': {'kind': 'commandRejected', 'commandId': 'other'}}, 'c', 's')
        self.assertEqual(result['state'], 'unknown')
        result = msp.rejection({'code': -32030, 'data': {'kind': 'commandRejected', 'commandId': 'c', 'reason': 'private prompt text'}}, 'c', 's')
        self.assertNotIn('private', str(result))

    def test_unknown_marker_precedes_write_and_never_blindly_replays(self):
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            command = msp.uuid7()
            request = {'command_id': command, 'provider_session_id': 's', 'text': 'hello',
                       'fence': {'session_id': 'gc-test', 'continuation_epoch': '1', 'runtime_token': 'token'}}
            def failed_write(method, params):
                import json
                disk = json.loads(host.state_path.read_text())
                self.assertEqual(disk['receipts'][command]['state'], 'unknown')
                raise OSError('uncertain write')
            host.rpc = failed_write
            self.assertEqual(host.admit(request)['state'], 'unknown')
            host.rpc = lambda *_: self.fail('replayed unknown admission')
            self.assertEqual(host.admit(request)['state'], 'unknown')

    def test_changed_payload_and_fence_refused(self):
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            request = {'command_id': msp.uuid7(), 'provider_session_id': 's', 'text': 'hello',
                       'fence': {'session_id': 'gc-test', 'continuation_epoch': '1', 'runtime_token': 'token'}}
            host.rpc = lambda _, p: {'commandId': p['commandId'], 'status': 'accepted', 'turnId': 'distinct-turn', 'disposition': 'queued'}
            host.admit(request)
            with self.assertRaises(ValueError):
                host.admit(dict(request, text='different'))
            with self.assertRaises(ValueError):
                host.admit(dict(request, fence=dict(request['fence'], runtime_token='new')))
            self.assertNotIn('terminal', host.receipt(request['command_id']))
            host.state['events'].append({'method': 'turn/completed', 'turn_id': 'distinct-turn', 'terminal': 'failed'})
            self.assertEqual(host.receipt(request['command_id'])['terminal'], 'failed')

    def test_bound_log_recovery_only_correlated_terminal(self):
        import json
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            log = pathlib.Path(folder) / 'session.jsonl'
            host.state['session_path'] = str(log)
            host.state['receipts']['c'] = {'state': 'unknown', 'command_id': 'c', 'provider_session_id': 's'}
            record = {'stream': {'kind': 'session', 'id': 's'}, 'payload_type': 'runtime.command_intake.settled', 'payload': {'record': {'command_id': 'other', 'outcome': 'abandoned'}}}
            log.write_text(json.dumps(record) + '\n')
            host.reconcile()
            self.assertNotIn('terminal', host.receipt('c'))
            record['payload']['record']['command_id'] = 'c'
            log.write_text(json.dumps(record) + '\n')
            host.reconcile()
            self.assertEqual(host.receipt('c')['terminal'], 'abandoned')
            record['stream']['id'] = 'different'
            log.write_text(json.dumps(record) + '\n')
            with self.assertRaises(ValueError):
                host.reconcile()

    def test_launch_identity_and_options_are_explicit(self):
        key = msp.uuid7()
        options = msp.launch_options({'command': 'muse --session-id ' + key + ' --provider echo --reasoning-effort high --model test'})
        self.assertEqual(options['session_id'], key)
        self.assertEqual(options['reasoning'], 'high')
        self.assertEqual(options['model'], 'test')
        with self.assertRaises(ValueError):
            msp.launch_options({'command': 'muse --unknown hidden'})

    def test_new_command_presend_rejection_is_definitive(self):
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            host.rpc = lambda *_: self.fail('wrong host admitted')
            request = {'command_id': msp.uuid7(), 'provider_session_id': 'wrong', 'text': 'hello',
                       'fence': {'session_id': 'gc-test', 'continuation_epoch': '1', 'runtime_token': 'token'}}
            result = host.operation('admit', request, [])
            self.assertEqual(result['state'], 'not_admitted')
            self.assertEqual(result['fence'], request['fence'])
            self.assertEqual(host.state['receipts'], {})

    def test_positional_startup_prompt_and_yolo_posture(self):
        options = msp.launch_options({'command': 'muse --yolo -- "Run current duty; exit if none."'})
        self.assertEqual(options['prompt'], 'Run current duty; exit if none.')
        self.assertEqual(options['approval'], 'allowAll')
        self.assertTrue(options['disable_sandbox'])
        self.assertTrue(options['trust_workspace'])

    def test_server_request_is_denied_and_observable(self):
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            host.pending = {}
            replies = []
            host.write = replies.append
            host.dispatch({'id': 4, 'method': 'approval/request', 'params': {'private': 'text'}})
            self.assertEqual(replies[0]['error']['code'], -32601)
            self.assertEqual(host.state['blocked']['reason'], 'unsupported_server_request')
            self.assertNotIn('private', host.state_path.read_text())
            host.dispatch({'id': 5, 'method': 'approval/request', 'params': {'sessionId': 's', 'turnId': 'turn'}})
            host.dispatch({'method': 'turn/completed', 'params': {'sessionId': 's', 'turnId': 'other', 'terminal': 'failed'}})
            self.assertIn('blocked', host.state)
            host.dispatch({'method': 'turn/completed', 'params': {'sessionId': 's', 'turnId': 'turn', 'terminal': 'failed'}})
            self.assertNotIn('blocked', host.state)

    def test_conversation_epoch_not_process_token_owns_native_identity(self):
        state = {'meta': {'GC_SESSION_ID': 'gc-one', 'GC_CONTINUATION_EPOCH': '1', 'GC_INSTANCE_TOKEN': 'old'}}
        env = dict(state['meta'], GC_INSTANCE_TOKEN='new')
        self.assertFalse(msp.conversation_changed(state, env))
        self.assertTrue(msp.conversation_changed(state, dict(env, GC_CONTINUATION_EPOCH='2')))
        self.assertTrue(msp.conversation_changed(state, dict(env, GC_SESSION_ID='gc-other')))

    def test_startup_cancellation_is_fenced_to_host_token(self):
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            host.folder = pathlib.Path(folder)
            host.state['token'] = 'new-host'
            host.cancelled = threading.Event()
            msp.save(host.folder / 'cancel.json', {'token': 'previous-host'})
            self.assertFalse(host.startup_cancelled())
            msp.save(host.folder / 'cancel.json', {'token': 'new-host'})
            self.assertTrue(host.startup_cancelled())

    def test_stop_during_initialization_cancels_exact_host_and_waits(self):
        with tempfile.TemporaryDirectory() as folder:
            root = pathlib.Path(folder)
            msp.save(root / 'state.json', {'token': 'initializing'})
            with patch.object(msp.fcntl, 'flock', side_effect=[BlockingIOError(), None]), patch.object(msp.time, 'sleep'):
                msp.await_stop(root, cancel=True)
            import json
            self.assertEqual(json.loads((root / 'cancel.json').read_text()), {'token': 'initializing'})

    def test_legacy_mutations_fail_and_optional_activity_is_empty(self):
        for operation in ('nudge', 'interrupt', 'send-keys'):
            with patch.object(msp.sys, 'argv', ['adapter', operation, 'target']), patch.object(msp.sys, 'stderr', io.StringIO()):
                self.assertEqual(msp.cli(), 1)
        with patch.object(msp.sys, 'argv', ['adapter', 'get-last-activity', 'target']), patch.object(msp.sys, 'stdout', io.StringIO()) as output:
            self.assertEqual(msp.cli(), 0)
            self.assertEqual(output.getvalue(), '')

    def test_staging_cannot_escape_workspace(self):
        with tempfile.TemporaryDirectory() as folder:
            with self.assertRaises(ValueError):
                msp.stage({'work_dir': folder, 'copy_files': [{'src': 'unused', 'rel_dst': '../escape'}]}, {})

    def test_initial_conditional_admission_requires_no_observed_run_or_pending_work(self):
        for observed, pending, allowed in ((None, 0, True), ('untracked-run', 0, False), (None, 1, False)):
            with self.subTest(observed=observed, pending=pending), tempfile.TemporaryDirectory() as folder:
                host = self.host(folder)
                fence = {'session_id': 'gc-test', 'continuation_epoch': '1', 'runtime_token': 'token'}
                host.state['fence'] = fence
                host.reconcile = lambda: None
                host.status = lambda: {'pending_count': pending, 'last_command_id': None, 'last_observed_run_id': observed}
                sent = []
                def rpc(method, params):
                    sent.append(params)
                    return {'commandId': params['commandId'], 'status': 'accepted', 'turnId': 'turn', 'disposition': 'started'}
                host.rpc = rpc
                request = {'command_id': msp.uuid7(), 'provider_session_id': 's', 'fence': fence,
                           'expected_command_id': None, 'text': 'reconcile'}
                if allowed:
                    self.assertEqual(host.admit(request, conditional=True)['state'], 'accepted')
                    self.assertEqual(len(sent), 1)
                else:
                    with self.assertRaises(ValueError):
                        host.admit(request, conditional=True)
                    self.assertEqual(sent, [])
                    self.assertEqual(host.state['receipts'], {})

    def test_conditional_recovery_refuses_newer_run(self):
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            host.child = type('Child', (), {'poll': lambda _: None})()
            fence = {'session_id': 'gc-test', 'continuation_epoch': '1', 'runtime_token': 'token'}
            host.state.update(fence=fence, last_command_id='old', last_observed_run_id='new-turn')
            host.state['receipts']['old'] = {'state': 'accepted', 'turn_id': 'old-turn', 'terminal': 'failed'}
            host.reconcile = lambda: None
            host.rpc = lambda *_: self.fail('recovery submitted after newer run')
            with self.assertRaises(ValueError):
                host.admit({'command_id': msp.uuid7(), 'provider_session_id': 's', 'fence': fence,
                            'text': 'reconcile', 'expected_command_id': 'old'}, conditional=True)

    def test_reordered_protocol_responses_and_wrong_session_notification(self):
        import queue
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            host.pending = {1: queue.Queue(), 2: queue.Queue()}
            host.dispatch({'id': 2, 'result': {'commandId': 'second'}})
            host.dispatch({'id': 1, 'result': {'commandId': 'first'}})
            self.assertEqual(host.pending[1].get_nowait()['result']['commandId'], 'first')
            self.assertEqual(host.pending[2].get_nowait()['result']['commandId'], 'second')
            host.dispatch({'method': 'turn/completed', 'params': {'sessionId': 'other', 'turnId': 'c', 'terminal': 'completed'}})
            self.assertEqual(host.state['events'], [])
            host.dispatch({'method': 'turn/completed', 'params': {'sessionId': 's', 'turnId': 'c', 'terminal': 'failed', 'output': 'secret'}})
            self.assertNotIn('secret', host.state_path.read_text())

    def test_crash_unknown_recovers_durable_acceptance_without_inventing_turn(self):
        import json
        with tempfile.TemporaryDirectory() as folder:
            host = self.host(folder)
            log = pathlib.Path(folder) / 'session.jsonl'
            host.state['session_path'] = str(log)
            host.state['receipts']['c'] = {'state': 'unknown', 'command_id': 'c', 'provider_session_id': 's'}
            accepted = {'stream': {'kind': 'session', 'id': 's'}, 'sequence': 11, 'durability': 'durable', 'payload_type': 'runtime.user_intent.accepted',
                        'payload': {'intent_id': 'c', 'source_session_id': 's'}}
            materialized = dict(accepted, sequence=13, payload_type='runtime.user_intent.materialized',
                                payload={'intent_id': 'c', 'source_session_id': 's', 'outcome': {'kind': 'terminal_no_effect'}})
            log.write_text(json.dumps(accepted) + '\n' + json.dumps(materialized) + '\n')
            host.reconcile()
            receipt = host.receipt('c')
            self.assertEqual(receipt['state'], 'accepted')
            self.assertEqual(receipt['receipt_id'], 's:11')
            self.assertEqual(receipt['terminal'], 'terminal_no_effect')
            self.assertNotIn('turn_id', receipt)


if __name__ == '__main__':
    unittest.main()
