defmodule Realtime.Test.Tokens do
  @moduledoc "Mints socket tokens for tests."

  def secret, do: Application.fetch_env!(:realtime, :auth_secret)

  def uuid do
    <<a::32, b::16, c::16, d::16, e::48>> = :crypto.strong_rand_bytes(16)

    :io_lib.format("~8.16.0b-~4.16.0b-~4.16.0b-~4.16.0b-~12.16.0b", [a, b, c, d, e])
    |> IO.iodata_to_binary()
  end

  def ws_claims(overrides \\ %{}) do
    Map.merge(
      %{
        "purpose" => "ws",
        "sub" => uuid(),
        "kind" => "user",
        "wid" => uuid(),
        "role" => "agent",
        "name" => "Anna",
        "avatar" => "https://example.test/a.png",
        "sid" => uuid(),
        "jti" => uuid(),
        "iat" => now(),
        "iat_ms" => now_ms(),
        "exp" => now() + 300
      },
      overrides
    )
  end

  def widget_claims(overrides \\ %{}) do
    Map.merge(
      %{
        "purpose" => "widget_ws",
        "sub" => uuid(),
        "wid" => uuid(),
        "jti" => uuid(),
        "iat" => now(),
        "exp" => now() + 300
      },
      overrides
    )
  end

  def sign(claims, opts \\ []) do
    alg = Keyword.get(opts, :alg, "HS256")
    key = Keyword.get(opts, :secret, secret())
    jwk = JOSE.JWK.from_oct(key)
    {_, token} = JOSE.JWT.sign(jwk, %{"alg" => alg}, claims) |> JOSE.JWS.compact()
    token
  end

  @doc "An unsigned token (`alg: none`)."
  def unsigned(claims) do
    enc = fn map -> map |> Jason.encode!() |> Base.url_encode64(padding: false) end
    enc.(%{"alg" => "none", "typ" => "JWT"}) <> "." <> enc.(claims) <> "."
  end

  def random_ip, do: {10, :rand.uniform(254), :rand.uniform(254), :rand.uniform(254)}

  def now, do: System.system_time(:second)

  def now_ms, do: System.system_time(:millisecond)
end
