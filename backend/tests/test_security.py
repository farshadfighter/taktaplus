from app.core.security import decrypt_secret, encrypt_secret


def test_encrypt_decrypt_round_trip():
    plaintext = "super-secret-fortigate-api-token"
    token = encrypt_secret(plaintext)

    assert token != plaintext
    assert decrypt_secret(token) == plaintext


def test_encrypting_same_value_twice_gives_different_ciphertext():
    a = encrypt_secret("same-value")
    b = encrypt_secret("same-value")

    assert a != b  # random nonce per call
    assert decrypt_secret(a) == decrypt_secret(b) == "same-value"
